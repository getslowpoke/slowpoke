# Go: net/http, Gin, Echo, database/sql, GORM

Go has no automatic agent: wrap the HTTP handler and the database once, in `main`.

```go
import (
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"github.com/XSAM/otelsql"
)

func setupTracing(ctx context.Context) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint("127.0.0.1:4318"), otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// database/sql
db, err := otelsql.Open("pgx", dsn)

// HTTP (routes named by the pattern with Go 1.22+ ServeMux)
http.ListenAndServe(":8080", otelhttp.NewHandler(mux, "server"))
```

- Gin: `r.Use(otelgin.Middleware("shop-api"))` (`go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin`).
- Echo: `e.Use(otelecho.Middleware("shop-api"))`.
- GORM: `db.Use(tracing.NewPlugin())` (`gorm.io/plugin/opentelemetry/tracing`).
- pgx: `otelpgx.NewTracer()` as the connection `Tracer`.

Set `OTEL_SERVICE_NAME` in the environment.

`otelhttp` (0.71) does not send `http.route`, not even with Go 1.22 patterns: the agent names
the route from the path (`/orders/40919` becomes `/orders/{id}`). To send the pattern, set it
in a middleware after routing: `otelhttp.WithRouteTag` or
`trace.SpanFromContext(r.Context()).SetAttributes(semconv.HTTPRoute(r.Pattern))`.
