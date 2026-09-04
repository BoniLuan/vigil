package adminui

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func requestAsset(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestLandingPageAndAssets(t *testing.T) {
	handler, err := New(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("landing status = %d", response.Code)
	}
	for _, expected := range []string{
		"Know when your services need attention.",
		"Illustrative preview",
		"https://github.com/BoniLuan/vigil",
		"Operator login",
		"/assets/favicon.svg",
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("landing response does not contain %q", expected)
		}
	}

	assetRequest := httptest.NewRequest(http.MethodGet, "/assets/landing.css", nil)
	assetResponse := httptest.NewRecorder()
	mux.ServeHTTP(assetResponse, assetRequest)
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("asset status = %d", assetResponse.Code)
	}
	if favicon := requestAsset(t, mux, "/assets/favicon.svg"); favicon.Code != http.StatusOK {
		t.Fatalf("favicon status = %d", favicon.Code)
	}
	if contentType := assetResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "text/css") {
		t.Errorf("asset Content-Type = %q", contentType)
	}
}
