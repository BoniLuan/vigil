package adminui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/google/uuid"
)

//go:embed templates/*.html assets/*.css
var files embed.FS

type Handler struct {
	monitors  *monitor.Service
	results   *checkresult.Service
	logger    *slog.Logger
	templates map[string]*template.Template
}

type page struct {
	Title     string
	Monitors  []monitor.Monitor
	Monitor   monitor.Monitor
	Results   []checkresult.StoredResult
	Summaries []checkresult.Summary
	Form      formValues
	Errors    map[string]string
	Action    string
	IsEdit    bool
}

type formValues struct {
	Name, Slug, Description, URL, Method             string
	StatusMin, StatusMax, IntervalSeconds, TimeoutMS string
	FailureThreshold, RecoveryThreshold              string
	Public                                           bool
}

func New(monitors *monitor.Service, results *checkresult.Service, logger *slog.Logger) (*Handler, error) {
	functions := template.FuncMap{
		"timefmt": timeFormat, "duration": durationFormat, "percent": percentFormat,
		"hostname": hostname, "safeurl": safeURL, "errorcode": errorCode,
	}
	templates := make(map[string]*template.Template)
	for _, name := range []string{"landing", "list", "detail", "form"} {
		parsed, err := template.New(name).Funcs(functions).ParseFS(files, "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, err)
		}
		templates[name] = parsed
	}
	return &Handler{monitors: monitors, results: results, logger: logger, templates: templates}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	assets, _ := fs.Sub(files, "assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /{$}", h.landing)
	mux.HandleFunc("GET /monitors", h.list)
	mux.HandleFunc("GET /monitors/new", h.newForm)
	mux.HandleFunc("POST /monitors", h.create)
	mux.HandleFunc("GET /monitors/{id}", h.detail)
	mux.HandleFunc("GET /monitors/{id}/edit", h.editForm)
	mux.HandleFunc("POST /monitors/{id}", h.update)
	mux.HandleFunc("POST /monitors/{id}/pause", h.pause)
	mux.HandleFunc("POST /monitors/{id}/resume", h.resume)
	mux.HandleFunc("POST /monitors/{id}/archive", h.archive)
}

func (h *Handler) landing(w http.ResponseWriter, _ *http.Request) {
	h.render(w, "landing", page{Title: "Vigil — Monitoring with operational clarity"})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	values, err := h.monitors.List(r.Context(), monitor.ListOptions{Limit: 100})
	if err != nil {
		h.internal(w, "list monitors", err)
		return
	}
	h.render(w, "list", page{Title: "Monitors", Monitors: values})
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	value, err := h.monitors.Get(r.Context(), id)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	results, err := h.results.List(r.Context(), id, checkresult.ListOptions{Limit: 25})
	if err != nil {
		h.internal(w, "list monitor history", err)
		return
	}
	summaries, err := h.results.Summaries(r.Context(), id)
	if err != nil {
		h.internal(w, "summarize monitor", err)
		return
	}
	h.render(w, "detail", page{Title: value.Name, Monitor: value, Results: results, Summaries: summaries})
}

func (h *Handler) newForm(w http.ResponseWriter, _ *http.Request) {
	h.render(w, "form", page{Title: "New monitor", Action: "/monitors", Form: defaultForm()})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	form, fields := parseForm(w, r)
	if len(fields) > 0 {
		h.renderStatus(w, "form", page{Title: "New monitor", Action: "/monitors", Form: form, Errors: fields}, http.StatusBadRequest)
		return
	}
	value, err := h.monitors.Create(r.Context(), form.createInput())
	if err != nil {
		if validation := formErrors(err); validation != nil {
			h.renderStatus(w, "form", page{Title: "New monitor", Action: "/monitors", Form: form, Errors: validation}, http.StatusBadRequest)
			return
		}
		if errors.Is(err, monitor.ErrSlugConflict) {
			h.renderStatus(w, "form", page{Title: "New monitor", Action: "/monitors", Form: form, Errors: map[string]string{"slug": "must be unique"}}, http.StatusConflict)
			return
		}
		h.internal(w, "create monitor", err)
		return
	}
	http.Redirect(w, r, "/monitors/"+value.ID.String(), http.StatusSeeOther)
}

func (h *Handler) editForm(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	value, err := h.monitors.Get(r.Context(), id)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	if value.ArchivedAt != nil {
		http.Error(w, "archived monitors cannot be edited", http.StatusConflict)
		return
	}
	h.render(w, "form", page{Title: "Edit " + value.Name, Action: "/monitors/" + id.String(), IsEdit: true, Monitor: value, Form: formFromMonitor(value)})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	current, err := h.monitors.Get(r.Context(), id)
	if err != nil {
		h.serviceError(w, err)
		return
	}
	form, fields := parseForm(w, r)
	view := page{Title: "Edit " + current.Name, Action: "/monitors/" + id.String(), IsEdit: true, Monitor: current, Form: form, Errors: fields}
	if len(fields) > 0 {
		h.renderStatus(w, "form", view, http.StatusBadRequest)
		return
	}
	_, err = h.monitors.Update(r.Context(), id, form.patchInput())
	if err != nil {
		if validation := formErrors(err); validation != nil {
			view.Errors = validation
			h.renderStatus(w, "form", view, http.StatusBadRequest)
			return
		}
		if errors.Is(err, monitor.ErrSlugConflict) {
			view.Errors = map[string]string{"slug": "must be unique"}
			h.renderStatus(w, "form", view, http.StatusConflict)
			return
		}
		h.serviceError(w, err)
		return
	}
	http.Redirect(w, r, "/monitors/"+id.String(), http.StatusSeeOther)
}

func (h *Handler) pause(w http.ResponseWriter, r *http.Request)  { h.command(w, r, h.monitors.Pause) }
func (h *Handler) resume(w http.ResponseWriter, r *http.Request) { h.command(w, r, h.monitors.Resume) }
func (h *Handler) command(w http.ResponseWriter, r *http.Request, command func(context.Context, uuid.UUID) (monitor.Monitor, error)) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	if _, err := command(r.Context(), id); err != nil {
		h.serviceError(w, err)
		return
	}
	http.Redirect(w, r, "/monitors/"+id.String(), http.StatusSeeOther)
}

func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil || r.Form.Get("confirm") != "archive" {
		http.Error(w, "archive confirmation is required", http.StatusBadRequest)
		return
	}
	if err := h.monitors.Delete(r.Context(), id); err != nil {
		h.serviceError(w, err)
		return
	}
	http.Redirect(w, r, "/monitors", http.StatusSeeOther)
}

func (h *Handler) id(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}
func (h *Handler) render(w http.ResponseWriter, name string, data page) {
	h.renderStatus(w, name, data, http.StatusOK)
}
func (h *Handler) renderStatus(w http.ResponseWriter, name string, data page, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := h.templates[name].ExecuteTemplate(w, name+".html", data); err != nil {
		h.logger.Error("render admin template failed", "template", name, "error", err)
	}
}
func (h *Handler) serviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, monitor.ErrNotFound):
		http.Error(w, "monitor not found", http.StatusNotFound)
	case errors.Is(err, monitor.ErrArchived):
		http.Error(w, "monitor is archived", http.StatusConflict)
	default:
		h.internal(w, "monitor operation", err)
	}
}
func (h *Handler) internal(w http.ResponseWriter, operation string, err error) {
	h.logger.Error("admin request failed", "operation", operation, "error", err)
	http.Error(w, "an unexpected error occurred", http.StatusInternalServerError)
}

func parseForm(w http.ResponseWriter, r *http.Request) (formValues, map[string]string) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		return formValues{}, map[string]string{"form": "invalid form submission"}
	}
	form := formValues{Name: r.Form.Get("name"), Slug: r.Form.Get("slug"), Description: r.Form.Get("description"), URL: r.Form.Get("url"), Method: r.Form.Get("method"), StatusMin: r.Form.Get("status_min"), StatusMax: r.Form.Get("status_max"), IntervalSeconds: r.Form.Get("interval_seconds"), TimeoutMS: r.Form.Get("timeout_ms"), FailureThreshold: r.Form.Get("failure_threshold"), RecoveryThreshold: r.Form.Get("recovery_threshold"), Public: r.Form.Has("public")}
	fields := map[string]string{}
	for key, raw := range map[string]string{"expected_status_min": form.StatusMin, "expected_status_max": form.StatusMax, "interval_seconds": form.IntervalSeconds, "timeout_ms": form.TimeoutMS, "failure_threshold": form.FailureThreshold, "recovery_threshold": form.RecoveryThreshold} {
		if value, err := strconv.Atoi(raw); err != nil || value <= 0 {
			fields[key] = "must be a positive whole number"
		}
	}
	return form, fields
}
func (f formValues) createInput() monitor.CreateInput {
	description := optionalDescription(f.Description)
	return monitor.CreateInput{Name: f.Name, Slug: f.Slug, Description: description, URL: f.URL, HTTPMethod: f.Method, ExpectedStatusMin: atoi(f.StatusMin), ExpectedStatusMax: atoi(f.StatusMax), Interval: time.Duration(atoi(f.IntervalSeconds)) * time.Second, Timeout: time.Duration(atoi(f.TimeoutMS)) * time.Millisecond, FailureThreshold: atoi(f.FailureThreshold), RecoveryThreshold: atoi(f.RecoveryThreshold), Public: f.Public}
}
func (f formValues) patchInput() monitor.PatchInput {
	name, slug, rawURL, method := f.Name, f.Slug, f.URL, f.Method
	statusMin, statusMax, failures, recoveries := atoi(f.StatusMin), atoi(f.StatusMax), atoi(f.FailureThreshold), atoi(f.RecoveryThreshold)
	interval, timeout := time.Duration(atoi(f.IntervalSeconds))*time.Second, time.Duration(atoi(f.TimeoutMS))*time.Millisecond
	public := f.Public
	description := optionalDescription(f.Description)
	return monitor.PatchInput{Name: &name, Slug: &slug, Description: monitor.OptionalString{Set: true, Value: description}, URL: &rawURL, HTTPMethod: &method, ExpectedStatusMin: &statusMin, ExpectedStatusMax: &statusMax, Interval: &interval, Timeout: &timeout, FailureThreshold: &failures, RecoveryThreshold: &recoveries, Public: &public}
}
func defaultForm() formValues {
	return formValues{Method: monitor.MethodGET, StatusMin: "200", StatusMax: "200", IntervalSeconds: "60", TimeoutMS: "2000", FailureThreshold: "3", RecoveryThreshold: "1"}
}
func formFromMonitor(m monitor.Monitor) formValues {
	description := ""
	if m.Description != nil {
		description = *m.Description
	}
	return formValues{Name: m.Name, Slug: m.Slug, Description: description, URL: m.URL, Method: m.HTTPMethod, StatusMin: strconv.Itoa(m.ExpectedStatusMin), StatusMax: strconv.Itoa(m.ExpectedStatusMax), IntervalSeconds: strconv.Itoa(int(m.Interval.Seconds())), TimeoutMS: strconv.Itoa(int(m.Timeout.Milliseconds())), FailureThreshold: strconv.Itoa(m.FailureThreshold), RecoveryThreshold: strconv.Itoa(m.RecoveryThreshold), Public: m.Public}
}
func optionalDescription(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
func atoi(value string) int { result, _ := strconv.Atoi(value); return result }
func formErrors(err error) map[string]string {
	var validation *monitor.ValidationError
	if errors.As(err, &validation) {
		return validation.Fields
	}
	return nil
}
func timeFormat(value *time.Time) string {
	if value == nil {
		return "N/A"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}
func durationFormat(value *time.Duration) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%d ms", value.Milliseconds())
}
func percentFormat(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f%%", *value)
}
func hostname(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "invalid host"
	}
	return parsed.Hostname()
}
func safeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "invalid URL"
	}
	parsed.RawQuery, parsed.Fragment, parsed.User = "", "", nil
	return parsed.String()
}
func errorCode(result checkresult.StoredResult) string {
	if result.Error == nil {
		return "—"
	}
	return string(result.Error.Code)
}
