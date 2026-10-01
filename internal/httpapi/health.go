package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type depStatus struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// checkDependencies actually contacts the database and (if configured) Redis,
// rather than just confirming the process is listening. An uptime monitor
// pointed at /healthz catches real outages this way, not only a crashed process.
func (a *API) checkDependencies(ctx context.Context) map[string]depStatus {
	out := map[string]depStatus{}
	if err := a.store.Ping(ctx); err != nil {
		out["database"] = depStatus{Error: err.Error()}
	} else {
		out["database"] = depStatus{OK: true}
	}
	if a.cfg.RedisURL != "" {
		if err := pingRedis(ctx, a.cfg.RedisURL); err != nil {
			out["redis"] = depStatus{Error: err.Error()}
		} else {
			out["redis"] = depStatus{OK: true}
		}
	}
	return out
}

func pingRedis(ctx context.Context, url string) error {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return err
	}
	c := redis.NewClient(opt)
	defer c.Close()
	return c.Ping(ctx).Err()
}

// healthz is a liveness+readiness check: 200 only when every configured
// dependency actually answers, so an external uptime monitor pointed here
// catches a down database/Redis, not just a crashed process.
func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	deps := a.checkDependencies(ctx)
	code := http.StatusOK
	for _, d := range deps {
		if !d.OK {
			code = http.StatusServiceUnavailable
			break
		}
	}
	writeJSON(w, code, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[code == http.StatusOK], "dependencies": deps})
}
