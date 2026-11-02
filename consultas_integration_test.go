package catalogos_test

import (
	"context"
	"io/fs"
	"sort"
	"testing"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	platformerrors "github.com/AI27-Tech/pf-pkg-platform-go/errors"
	platformpgx "github.com/AI27-Tech/pf-pkg-platform-go/pgxpool"
	catalogos "github.com/AI27-Tech/pf-svc-catalogos"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/jackc/pgx/v5"
)

// TestConsultasListarTiposServicio llama la fachada en proceso como lo hará core
// para ListarTiposServicio del bff (E9-T4): ListarValoresCatalogo con el tipo
// fijo tipos_servicio devuelve solo los valores de ese tipo, y un tipo fuera del
// enum es Validacion, igual que GET /api/v1/catalogos-tipo/{tipo}.
func TestConsultasListarTiposServicio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)

	for tipo, codigo := range map[catclient.TipoCatalogo]string{
		catclient.TiposServicio:    "CUSTODIA-ARMADA",
		catclient.LineasTransporte: "LINEA-NORTE",
	} {
		if _, err := m.Comandos().CrearValorCatalogo(ctx, tipo, catclient.ValorCatalogoEntrada{
			Codigo: codigo, Etiqueta: codigo, Estado: catclient.Activo,
		}); err != nil {
			t.Fatalf("crear %s/%s: %v", tipo, codigo, err)
		}
	}

	pag, err := m.Consultas().ListarValoresCatalogo(ctx, catclient.TiposServicio, nil)
	if err != nil {
		t.Fatalf("ListarValoresCatalogo(tipos_servicio): %v", err)
	}
	if len(pag.Items) != 1 || pag.Items[0].Codigo != "CUSTODIA-ARMADA" || pag.CursorSiguiente != nil {
		t.Fatalf("got %+v, want un solo item CUSTODIA-ARMADA sin cursor siguiente", pag)
	}

	_, err = m.Consultas().ListarValoresCatalogo(ctx, "rutas", nil)
	if got := platformerrors.AsCategory(err); got != platformerrors.CategoryValidacion {
		t.Fatalf("tipo inválido: got categoría %v (err %v), want Validacion", got, err)
	}
}

// moduloConCatalogos aplica las migraciones del esquema (mismo setup que
// internal/tipos) y construye el módulo como core, con el pool svc_catalogos.
func moduloConCatalogos(t *testing.T, ctx context.Context) *catalogos.Module {
	t.Helper()
	const schema, role = "catalogos", "svc_catalogos"
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	limpiar := func(ctx context.Context) {
		dbtest.Exec(t, ctx, admin, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		dbtest.Exec(t, ctx, admin, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	}
	limpiar(ctx)
	files, err := fs.Glob(migrations.FS, schema+"/*.sql")
	if err != nil {
		t.Fatalf("listar migraciones: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		sqlBytes, err := migrations.FS.ReadFile(f)
		if err != nil {
			t.Fatalf("leer %s: %v", f, err)
		}
		dbtest.Exec(t, ctx, admin, string(sqlBytes))
	}
	pool, closePool := dbtest.PoolAs(t, ctx, role, schema)
	// Cleanup corre tras el cancel del llamador: limpia con su propio contexto.
	t.Cleanup(func() {
		closePool()
		limpiar(context.Background())
		closeAdmin()
	})
	m, err := catalogos.New(catalogos.Pools{Catalogos: &platformpgx.Pool{Pool: pool, Role: role}}, catalogos.Deps{})
	if err != nil {
		t.Fatalf("catalogos.New: %v", err)
	}
	return m
}
