package sqlite

import (
	"context"
	"log/slog"
	"time"
)

// SlogHandler writes structured logs into SQLite app_logs.
type SlogHandler struct {
	store *Store
	level slog.Leveler
	attrs []slog.Attr
	group string
}

func NewSlogHandler(store *Store, level slog.Leveler) *SlogHandler {
	if level == nil {
		level = slog.LevelInfo
	}
	return &SlogHandler{store: store, level: level}
}

func (h *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make(map[string]any, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		attrs[a.Key] = slogAttrValue(a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		attrs[key] = slogAttrValue(a.Value)
		return true
	})
	ts := r.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	return h.store.InsertLog(ctx, ts, r.Level.String(), r.Message, attrs)
}

func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &cp
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	cp := *h
	if h.group != "" {
		cp.group = h.group + "." + name
	} else {
		cp.group = name
	}
	return &cp
}

// slogAttrValue stringifies errors so JSON attrs are readable (errorString
// otherwise marshals as {}).
func slogAttrValue(v slog.Value) any {
	if v.Kind() == slog.KindAny {
		if err, ok := v.Any().(error); ok && err != nil {
			return err.Error()
		}
	}
	return v.Any()
}
