package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
)

func main() {
	smoke := flag.Bool("smoke-test", false, "run a built-in self-test and exit")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	if *smoke {
		if err := runSmokeTest(); err != nil {
			fmt.Fprintln(os.Stderr, "smoke-test FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("smoke-test PASSED")
		return
	}

	srv := NewService()
	mux := buildMux(srv)
	log.Printf("snowflake listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func buildMux(srv *Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /machines", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MachineID *int64 `json:"machineID"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, badRequest("invalid JSON body: %v", err))
			return
		}
		if req.MachineID == nil {
			writeError(w, badRequest("field 'machineID' is required"))
			return
		}
		m, err := srv.RegisterMachine(*req.MachineID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, m)
	})

	mux.HandleFunc("GET /machines", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"machines": srv.ListMachines()})
	})

	mux.HandleFunc("GET /machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		mid, err := parseMachineID(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		m, err := srv.GetMachine(mid)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})

	mux.HandleFunc("DELETE /machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		mid, err := parseMachineID(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		if err := srv.RemoveMachine(mid); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	})

	mux.HandleFunc("POST /machines/{id}/ids", func(w http.ResponseWriter, r *http.Request) {
		mid, err := parseMachineID(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		var req struct {
			Count *int `json:"count"`
		}
		if r.ContentLength != 0 {
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, badRequest("invalid JSON body: %v", err))
				return
			}
		}
		count := 1
		if req.Count != nil {
			count = *req.Count
		}
		ids, err := srv.Generate(mid, count)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = strconv.FormatUint(id, 10)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ids": out})
	})

	mux.HandleFunc("GET /ids/{id}", func(w http.ResponseWriter, r *http.Request) {
		ins, err := srv.Inspect(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ins)
	})

	return mux
}

func parseMachineID(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, badRequest("machine id in path must be an integer: %v", err)
	}
	return v, nil
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	if e, ok := err.(*statusErr); ok {
		writeJSON(w, e.code, map[string]any{"error": e.msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
}
