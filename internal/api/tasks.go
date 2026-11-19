package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/bgtask"
	"github.com/antresol-app/antresol/internal/library"
)

// BackgroundTask is one background worker of «Задачи»: its id (the web
// app names it: "trickplay", "segments", "loudness") and its progress.
type BackgroundTask struct {
	ID       string
	Progress func() bgtask.Progress
}

// NetTarget is one service «Состояние» checks the server reaches: its id
// (the web app shows Name when it does not know it) and an address any
// answer of which (even 401 or 404) means the way there works.
type NetTarget struct {
	ID   string
	Name string
	URL  string
}

// taskItem is a worker's row of GET /api/admin/tasks.
type taskItem struct {
	ID string `json:"id"`
	bgtask.Progress
}

// tasksView is GET /api/admin/tasks: what runs in the background, in one
// place (the settings of each stay on their own screens). Scan and
// voclean are absent when the server has none.
type tasksView struct {
	Scan    *library.JobStatus `json:"scan,omitempty"`
	Voclean *vocleanView       `json:"voclean,omitempty"`
	Workers []taskItem         `json:"workers"`
}

// netResult is one row of GET /api/admin/health/network.
type netResult struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Ms    int64  `json:"ms"`
	Error string `json:"error,omitempty"`
}

// netTimeout bounds each reachability check.
const netTimeout = 6 * time.Second

// mountTasks: the admin's «Задачи» and «Состояние → Сеть».
//
//   - GET /api/admin/tasks: tasksView;
//   - GET /api/admin/health/network: each NetTarget reached or not, with
//     the time it took (all at once, each within netTimeout).
func mountTasks(r chi.Router, deps Deps) {
	client := deps.NetClient
	if client == nil {
		client = &http.Client{Timeout: netTimeout}
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/admin/tasks", func(w http.ResponseWriter, r *http.Request) {
			out := tasksView{Workers: []taskItem{}}
			if deps.Jobs != nil {
				st := deps.Jobs.Status()
				out.Scan = &st
			}
			if deps.Voclean != nil && deps.DB != nil {
				v := &vocleanAPI{db: deps.DB, w: deps.Voclean}
				view, err := v.view(r.Context())
				if err != nil {
					internalErr(w, r, "tasks: voclean", err)
					return
				}
				out.Voclean = &view
			}
			for _, t := range deps.Background {
				if t.Progress != nil {
					out.Workers = append(out.Workers, taskItem{ID: t.ID, Progress: t.Progress()})
				}
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, out)
		})
		r.Get("/api/admin/health/network", func(w http.ResponseWriter, r *http.Request) {
			out := checkNetwork(r.Context(), client, deps.NetTargets)
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, out)
		})
	})
}

// checkNetwork asks every target at once.
func checkNetwork(ctx context.Context, client *http.Client, targets []NetTarget) []netResult {
	out := make([]netResult, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		out[i] = netResult{ID: t.ID, Name: t.Name}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, netTimeout)
			defer cancel()
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, t.URL, nil)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			resp, err := client.Do(req)
			out[i].Ms = time.Since(start).Milliseconds()
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			resp.Body.Close()
			// any answer: the way there works (a key or a path is another matter)
			out[i].OK = true
		})
	}
	wg.Wait()
	return out
}
