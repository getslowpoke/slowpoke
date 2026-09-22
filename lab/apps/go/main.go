package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var db *sql.DB

func main() {
	ctx := context.Background()
	exp, err := otlptracehttp.New(ctx) // endpoint from OTEL_EXPORTER_OTLP_ENDPOINT
	if err != nil {
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(ctx)

	db, err = otelsql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(5)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { reply(w, map[string]string{"app": "go-shop"}) })
	mux.HandleFunc("GET /orders/{id}", orderByID)
	mux.HandleFunc("GET /stats", stats)
	mux.HandleFunc("GET /customers/{id}/spend", spend)
	mux.HandleFunc("GET /orders/recent", recent)
	// The payment gateway the Laravel shop waits on at checkout: slow on purpose.
	mux.HandleFunc("POST /gateway/pay", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1100 * time.Millisecond)
		reply(w, map[string]bool{"paid": true})
	})

	log.Println("go-shop on 8080")
	log.Fatal(http.ListenAndServe(":8080", otelhttp.NewHandler(mux, "go-shop")))
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func orderByID(w http.ResponseWriter, r *http.Request) {
	var id int
	var status string
	_ = db.QueryRowContext(r.Context(), "SELECT id, status FROM orders WHERE id = $1", r.PathValue("id")).Scan(&id, &status)
	reply(w, map[string]any{"id": id, "status": status})
}

// COUNT on the whole table plus a NOT IN subquery.
func stats(w http.ResponseWriter, r *http.Request) {
	var n int
	_ = db.QueryRowContext(r.Context(),
		"SELECT COUNT(*) FROM orders WHERE customer_id NOT IN (SELECT id FROM customers WHERE city = 'Roma')").Scan(&n)
	reply(w, map[string]int{"orders_outside_rome": n})
}

// Implicit cast: customer_id compared with text.
func spend(w http.ResponseWriter, r *http.Request) {
	var total float64
	_ = db.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(total), 0) FROM orders WHERE customer_id::text = $1", r.PathValue("id")).Scan(&total)
	reply(w, map[string]float64{"spend": total})
}

// No index on created_at: sort of the whole table for 20 rows.
func recent(w http.ResponseWriter, r *http.Request) {
	rows, err := db.QueryContext(r.Context(), "SELECT id, total FROM orders ORDER BY created_at DESC LIMIT 20")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int
		var total float64
		_ = rows.Scan(&id, &total)
		out = append(out, map[string]any{"id": id, "total": total})
	}
	reply(w, out)
}
