package tipos_test

import (
	"context"
	"io/fs"
	"sort"
	"testing"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/tipos"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/jackc/pgx/v5"
)

// Pruebas de integración de los catálogos de tipo (E5-T3): verifican los
// criterios de aceptación que solo tienen sentido contra Postgres real — lectura
// por lote que devuelve versión (C4/C6), outbox en la misma transacción (P17) y,
// el criterio central de la historia, DESACTIVAR un valor sin romper la historia
// (E5-HU-023): la versión que regía antes queda cerrada pero intacta. Se saltan
// sin CATALOGOS_TEST_DB.
//
// CATALOGOS_TEST_DB debe ser un DSN con privilegios para CREATE ROLE/SCHEMA.

const (
	schema = "catalogos"
	role   = "svc_catalogos"
)

func setup(t *testing.T, ctx context.Context) (*tipos.Repositorio, func()) {
	t.Helper()
	admin, closeAdmin := dbtest.AdminConn(t, ctx)

	limpiar := func() {
		dbtest.Exec(t, ctx, admin, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		dbtest.Exec(t, ctx, admin, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	}
	limpiar()

	// Aplica todas las migraciones del esquema en orden de nombre (incluye 0006
	// y 0007, que crean valor_catalogo y sus grants).
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
		if _, err := admin.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("aplicar %s: %v", f, err)
		}
	}

	pool, closePool := dbtest.PoolAs(t, ctx, role, schema)
	repo := tipos.NewRepositorio(pool)

	return repo, func() {
		closePool()
		limpiar()
		closeAdmin()
	}
}

// cuentaOutbox devuelve cuántos eventos catalogo.actualizado hay para un agregado.
func cuentaOutbox(t *testing.T, ctx context.Context, agregadoID string) int {
	t.Helper()
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	var n int
	err := admin.QueryRow(ctx,
		`SELECT count(*) FROM catalogos.outbox WHERE tipo = 'catalogo.actualizado' AND agregado_id = $1`,
		agregadoID).Scan(&n)
	if err != nil {
		t.Fatalf("contar outbox: %v", err)
	}
	return n
}

// subtipoDelEvento devuelve el valor de payload->>'tipo' del evento más reciente
// de un agregado: confirma la decisión B2 (la lista viaja en el payload).
func subtipoDelEvento(t *testing.T, ctx context.Context, agregadoID string) string {
	t.Helper()
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	var sub string
	err := admin.QueryRow(ctx,
		`SELECT payload->>'tipo' FROM catalogos.outbox
		  WHERE tipo = 'catalogo.actualizado' AND agregado_id = $1
		  ORDER BY id DESC LIMIT 1`, agregadoID).Scan(&sub)
	if err != nil {
		t.Fatalf("leer subtipo del evento: %v", err)
	}
	return sub
}

// TestCrear_EmiteEventoConSubtipo: crear un valor inserta la versión 1 Y emite
// catalogo.actualizado con el subtipo = lista en el payload (P17 + B2).
func TestCrear_EmiteEventoConSubtipo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	v, err := repo.Crear(ctx, catclient.LineasTransporte, catclient.ValorCatalogoEntrada{
		Codigo: "TRANSPORTES-SA", Etiqueta: "Transportes S.A.", Estado: catclient.Activo,
	})
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	if v.Version == nil || *v.Version != 1 {
		t.Fatalf("se esperaba version 1, fue %v", v.Version)
	}
	if n := cuentaOutbox(t, ctx, v.Id.String()); n != 1 {
		t.Fatalf("se esperaba 1 evento en outbox, hubo %d (P17)", n)
	}
	if sub := subtipoDelEvento(t, ctx, v.Id.String()); sub != "lineas_transporte" {
		t.Fatalf("el subtipo del evento debía ser lineas_transporte, fue %q (B2)", sub)
	}
}

// TestDesactivar_NoRompeLaHistoria es el criterio central de E5-HU-023:
// desactivar un valor crea la versión 2 (inactivo), pero la versión 1 (activo)
// queda intacta y cerrada por vigencia — un registro histórico que la usaba la
// sigue encontrando. La lectura por lote ya solo devuelve la vigente (inactiva),
// con versión 2 (C4/C6), y hay un segundo evento (P17).
func TestDesactivar_NoRompeLaHistoria(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	v1, err := repo.Crear(ctx, catclient.TiposServicio, catclient.ValorCatalogoEntrada{
		Codigo: "CUSTODIA-ARMADA", Etiqueta: "Custodia armada", Estado: catclient.Activo,
	})
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}

	// Desactivar = PATCH con estado inactivo por la misma vía.
	inactivo := catclient.ValorCatalogoCambiosEstadoInactivo
	v2, err := repo.Actualizar(ctx, catclient.TiposServicio, v1.Id,
		catclient.ValorCatalogoCambios{Estado: &inactivo}, time.Now())
	if err != nil {
		t.Fatalf("Actualizar (desactivar): %v", err)
	}
	if v2.Version == nil || *v2.Version != 2 {
		t.Fatalf("se esperaba version 2, fue %v", v2.Version)
	}
	if v2.Estado != catclient.ValorCatalogoEstadoInactivo {
		t.Fatalf("la nueva versión debía estar inactiva, fue %q", v2.Estado)
	}

	// La historia sigue intacta: la versión 1 existe, estaba activa y quedó
	// cerrada por vigencia (vigente_hasta no nulo). Se consulta como admin.
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	var (
		estadoV1       string
		vigenteHastaV1 *time.Time
	)
	err = admin.QueryRow(ctx, `
		SELECT estado, vigente_hasta FROM catalogos.valor_catalogo
		 WHERE id = $1 AND version = 1`, v1.Id).Scan(&estadoV1, &vigenteHastaV1)
	if err != nil {
		t.Fatalf("leer versión 1 histórica: %v", err)
	}
	if estadoV1 != "activo" {
		t.Fatalf("la versión 1 debía conservar su estado activo, fue %q (P8)", estadoV1)
	}
	if vigenteHastaV1 == nil {
		t.Fatalf("la versión 1 debía quedar cerrada por vigencia tras desactivar")
	}

	// La lectura por lote devuelve la vigente (inactiva), versión 2 (C4/C6).
	pag, err := repo.Listar(ctx, catclient.TiposServicio, nil)
	if err != nil {
		t.Fatalf("Listar: %v", err)
	}
	if len(pag.Items) != 1 {
		t.Fatalf("se esperaba 1 valor vigente, hubo %d", len(pag.Items))
	}
	if pag.Items[0].Version == nil || *pag.Items[0].Version != 2 {
		t.Fatalf("la vigente debía ser la versión 2, fue %v", pag.Items[0].Version)
	}
	if pag.Items[0].Estado != catclient.ValorCatalogoEstadoInactivo {
		t.Fatalf("la vigente debía estar inactiva, fue %q", pag.Items[0].Estado)
	}

	// Dos eventos: uno por el alta, uno por la desactivación (P17).
	if n := cuentaOutbox(t, ctx, v1.Id.String()); n != 2 {
		t.Fatalf("se esperaban 2 eventos en outbox, hubo %d", n)
	}
}

// TestListar_SoloElTipoPedido: la lectura por lote devuelve solo los valores del
// `tipo` consultado, en su versión vigente, sin paginar (cursor_siguiente nil).
func TestListar_SoloElTipoPedido(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	if _, err := repo.Crear(ctx, catclient.TiposUnidad, catclient.ValorCatalogoEntrada{
		Codigo: "PATRULLA", Etiqueta: "Patrulla", Estado: catclient.Activo,
	}); err != nil {
		t.Fatalf("Crear tipos_unidad: %v", err)
	}
	if _, err := repo.Crear(ctx, catclient.ZonasRiesgo, catclient.ValorCatalogoEntrada{
		Codigo: "TRAMO-X", Etiqueta: "Tramo X", Estado: catclient.Activo,
	}); err != nil {
		t.Fatalf("Crear zonas_riesgo: %v", err)
	}

	pag, err := repo.Listar(ctx, catclient.TiposUnidad, nil)
	if err != nil {
		t.Fatalf("Listar: %v", err)
	}
	if len(pag.Items) != 1 {
		t.Fatalf("se esperaba 1 valor de tipos_unidad, hubo %d", len(pag.Items))
	}
	if pag.Items[0].Codigo != "PATRULLA" {
		t.Fatalf("se esperaba el valor PATRULLA, fue %q", pag.Items[0].Codigo)
	}
	if pag.CursorSiguiente != nil {
		t.Fatalf("una sola página no debía devolver cursor_siguiente")
	}
}

// TestActualizar_TipoInvalido: un tipo fuera del enum se rechaza antes de tocar
// la BD (lo que el handler traduce a 400).
func TestActualizar_TipoInvalido(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	_, err := repo.Listar(ctx, catclient.TipoCatalogo("desconocido"), nil)
	if err == nil {
		t.Fatal("un tipo inválido debía dar error")
	}
}
