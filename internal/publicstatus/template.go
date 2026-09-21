package publicstatus

import (
	"fmt"
	"html/template"
)

func uptimePercent(successes, total int64) string {
	return fmt.Sprintf("%.2f%%", float64(successes)*100/float64(total))
}

var statusTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="30"><title>Service status · Vigil</title><link rel="icon" href="https://vigil.boniluan.com/assets/favicon.svg" type="image/svg+xml">
<style>:root{color-scheme:dark;font:16px/1.55 system-ui,sans-serif;background:#08130f;color:#eaf5ef}*{box-sizing:border-box}body{margin:0}header,main,footer{max-width:960px;margin:auto;padding:24px}header{display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #284238}a{color:#72dfac;text-decoration:none}main{padding-top:56px;padding-bottom:64px}h1{font-size:clamp(2.2rem,6vw,3.5rem);line-height:1.1;margin:0 0 14px}h2{font-size:1rem;margin:0}.muted{color:#9bb7a9}.eyebrow{color:#72dfac;letter-spacing:.13em;text-transform:uppercase;font-size:.76rem;font-weight:700}.banner,.row,.empty{border:1px solid #284238;border-radius:10px;background:#102119;padding:20px;margin-top:16px}.banner{margin:28px 0 36px;font-weight:700}.banner.good{border-color:#327e59;color:#8ee9b6}.banner.attention{border-color:#916b43;color:#f5ca91}.row{display:flex;justify-content:space-between;align-items:center;gap:20px}.row p{margin:5px 0 0}.state{font-weight:800;text-transform:uppercase;letter-spacing:.06em;font-size:.8rem}.state-up{color:#8ee9b6}.state-down{color:#ff9da5}.state-pending{color:#f5ca91}.state-paused{color:#bdc9c3}.stats{text-align:right;white-space:nowrap}footer{border-top:1px solid #284238;color:#9bb7a9;font-size:.85rem}@media(max-width:600px){.row{align-items:flex-start;flex-direction:column}.stats{text-align:left}}
</style></head><body><header><strong>Vigil · Status</strong><a href="https://vigil.boniluan.com/">About Vigil ↗</a></header><main><p class="eyebrow">Live service health</p><h1>Service status</h1><p class="muted">Only services explicitly marked public by the operator appear here. Updated {{.Updated}}; refreshes every 30 seconds.</p>
{{if .Services}}{{if .AllOperational}}<div class="banner good">All listed services are operational.</div>{{else}}<div class="banner attention">One or more listed services need attention.</div>{{end}}<section aria-label="Public services">{{range .Services}}<article class="row"><div><h2>{{.Name}}</h2><p class="muted">Last checked: {{.LastChecked}}</p></div><div class="stats"><span class="state state-{{.State}}">{{.State}}</span><p class="muted">24h uptime: {{.Uptime}}</p></div></article>{{end}}</section>{{else}}<div class="empty">No public services are listed yet.</div>{{end}}
<p class="muted">Uptime is successful completed checks divided by all completed checks in the last 24 hours. Pending and paused periods are not counted as failures.</p></main><footer>Vigil reports observed checks, not a guarantee of continuous availability. Incidents and maintenance notices are planned for a later release.</footer></body></html>`))
