package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	_ "embed"

	"github.com/c0dev0id/claude-live-coding/internal/event"
)

//go:embed static/index.html
var indexHTML []byte

type hub struct {
	mu      sync.Mutex
	history []event.Event
	subs    map[chan event.Event]struct{}
}

func newHub() *hub {
	return &hub{subs: make(map[chan event.Event]struct{})}
}

func (h *hub) publish(e event.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, e)
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (h *hub) subscribe() ([]event.Event, chan event.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan event.Event, 32)
	h.subs[ch] = struct{}{}
	snap := make([]event.Event, len(h.history))
	copy(snap, h.history)
	return snap, ch
}

func (h *hub) unsubscribe(ch chan event.Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) reset() {
	h.mu.Lock()
	h.history = nil
	h.mu.Unlock()
}

func writeSSE(w http.ResponseWriter, e event.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	h := newHub()

	http.HandleFunc("POST /ingest", func(w http.ResponseWriter, r *http.Request) {
		var e event.Event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if e.Ts == 0 {
			e.Ts = time.Now().UnixMilli()
		}
		h.publish(e)
		w.WriteHeader(http.StatusNoContent)
	})

	http.HandleFunc("DELETE /ingest", func(w http.ResponseWriter, r *http.Request) {
		h.reset()
		w.WriteHeader(http.StatusNoContent)
	})

	http.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		history, ch := h.subscribe()
		defer h.unsubscribe(ch)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		for _, e := range history {
			if err := writeSSE(w, e); err != nil {
				return
			}
		}
		flusher.Flush()

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				if err := writeSSE(w, e); err != nil {
					return
				}
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprintf(w, ": ping\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
