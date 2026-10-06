// Command api es el arranque standalone de catalogos (invariante I5): compila y
// sirve /health y /ready SIN core, cableando los adaptadores HTTP del módulo.
// Producción no lo usa para catalogos (vive dentro de core); existe para que el
// módulo se pueda levantar y probar de forma aislada.
//
// Scaffolding: lee entorno, abre el pool de svc_catalogos (si hay DATABASE_URL),
// construye el módulo y sirve health/ready. Aún no monta rutas de negocio.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/AI27-Tech/pf-pkg-platform-go/config"
	"github.com/AI27-Tech/pf-pkg-platform-go/observability"
	"github.com/AI27-Tech/pf-pkg-platform-go/pgxpool"
	"github.com/AI27-Tech/pf-pkg-platform-go/server"
	"github.com/AI27-Tech/pf-svc-catalogos"
)

func main() {
	if err := run(); err != nil {
		slog.Error("catalogos terminó con error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// catalogos necesita su base de datos para arrancar de verdad; se declara
	// como requerida para fallar temprano en el standalone.
	cfg, err := config.Load(config.EnvDatabaseURL)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("arrancando catalogos (standalone)", slog.String("service", cfg.ServiceName))

	shutdownOtel, err := observability.Init(ctx, observability.Config{
		ServiceName: cfg.ServiceName,
		ExporterURL: cfg.OtelExporterURL,
	})
	if err != nil {
		logger.Warn("observabilidad no inicializada", slog.Any("error", err))
	} else {
		defer func() { _ = shutdownOtel(context.Background()) }()
	}

	// Un pool para el único rol del esquema (00 regla 4).
	pool, err := pgxpool.New(ctx, "svc_catalogos", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// TODO(catalogos): aplicar migrations.FS con el rol svc_catalogos antes de
	// servir tráfico. Se hace aquí (y en core) porque platform-go no administra
	// schemas; se deja pendiente hasta tener el aplicador de migraciones acordado.

	mod, err := catalogos.New(catalogos.Pools{Catalogos: pool}, catalogos.Deps{})
	if err != nil {
		return err
	}

	srv := server.New(cfg.HTTPAddr, server.WithLogger(logger))
	mod.Routes(srv.Mux())
	srv.Registry().RegisterCheck("postgres", pool.CheckFunc())

	srv.MarkReady()
	return srv.Start(ctx)
}
