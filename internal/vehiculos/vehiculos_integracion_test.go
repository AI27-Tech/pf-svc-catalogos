package vehiculos_test

import (
	"context"
	"io/fs"
	"sort"
	"testing"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/vehiculos"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pruebas de integración de la escritura de vehículos (C7): verifican los
// criterios de aceptación de E5-T1 que solo tienen sentido contra Postgres real
// — versionado por vigencia (P8), lectura "a una fecha" (C9) y outbox en la
// misma transacción (P17). Se saltan sin CATALOGOS_TEST_DB.
//
// CATALOGOS_TEST_DB debe ser un DSN con privilegios para CREATE ROLE/SCHEMA.

const (
	schema = "catalogos"
	role   = "svc_catalogos"
)

func setup(t *testing.T, ctx context.Context) (*vehiculos.Repositorio, func()) {
	t.Helper()
	admin, closeAdmin := dbtest.AdminConn(t, ctx)

	limpiar := func() {
		dbtest.Exec(t, ctx, admin, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		dbtest.Exec(t, ctx, admin, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	}
	limpiar()

	// Aplica todas las migraciones del esquema en orden de nombre.
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
	repo := vehiculos.NewRepositorio(pool)

	return repo, func() {
		closePool()
		limpiar()
		closeAdmin()
	}
}

// cuentaOutbox devuelve cuántos eventos catalogo.actualizado hay para un agregado.
func cuentaOutbox(t *testing.T, ctx context.Context, repo *vehiculos.Repositorio, agregadoID string) int {
	t.Helper()
	// Se consulta con un pool admin aparte para no depender del repo.
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

func str(s string) *string { return &s }

// TestCrear_EmiteEventoEnLaMismaTx: crear un vehículo inserta la versión 1 Y
// emite catalogo.actualizado — ambos presentes tras el commit (P17).
func TestCrear_EmiteEventoEnLaMismaTx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	cliente := uuid.New()
	v, err := repo.Crear(ctx, catclient.VehiculoEntrada{
		Tipo:      catclient.Coche,
		Estado:    catclient.Activa,
		ClienteId: cliente,
		Economico: str("ECO-1"),
	}, cliente)
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	if v.Version == nil || *v.Version != 1 {
		t.Fatalf("se esperaba version 1, fue %v", v.Version)
	}
	if n := cuentaOutbox(t, ctx, repo, v.Id.String()); n != 1 {
		t.Fatalf("se esperaba 1 evento en outbox, hubo %d (P17)", n)
	}
}

// TestActualizar_CreaNuevaVersionSinSobrescribir: modificar crea la versión 2,
// la versión 1 sigue consultable "a una fecha" anterior (P8), y hay un segundo
// evento (P17).
func TestActualizar_CreaNuevaVersionSinSobrescribir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	cliente := uuid.New()
	v1, err := repo.Crear(ctx, catclient.VehiculoEntrada{
		Tipo: catclient.Coche, Estado: catclient.Activa, ClienteId: cliente, Placas: str("AAA-111"),
	}, cliente)
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	antesDelCambio := time.Now()
	time.Sleep(10 * time.Millisecond)

	// Modificar placas → nueva versión.
	nuevasPlacas := catclient.VehiculoCambios{Placas: str("BBB-222")}
	v2, err := repo.Actualizar(ctx, v1.Id, nuevasPlacas, time.Now())
	if err != nil {
		t.Fatalf("Actualizar: %v", err)
	}
	if v2.Version == nil || *v2.Version != 2 {
		t.Fatalf("se esperaba version 2, fue %v", v2.Version)
	}
	if v2.Placas == nil || *v2.Placas != "BBB-222" {
		t.Fatalf("la nueva versión debía tener las placas nuevas, fue %v", v2.Placas)
	}

	// La versión vigente ahora es la 2.
	vigente, err := repo.Obtener(ctx, v1.Id, nil)
	if err != nil {
		t.Fatalf("Obtener vigente: %v", err)
	}
	if vigente.Version == nil || *vigente.Version != 2 {
		t.Fatalf("la vigente debía ser la versión 2, fue %v", vigente.Version)
	}

	// Leer "a una fecha" ANTES del cambio devuelve la versión 1 (P8/C9).
	historico, err := repo.Obtener(ctx, v1.Id, &antesDelCambio)
	if err != nil {
		t.Fatalf("Obtener a-fecha: %v", err)
	}
	if historico.Version == nil || *historico.Version != 1 {
		t.Fatalf("a-fecha anterior debía devolver la versión 1, fue %v", historico.Version)
	}
	if historico.Placas == nil || *historico.Placas != "AAA-111" {
		t.Fatalf("la versión 1 debía conservar sus placas originales, fue %v", historico.Placas)
	}

	// Dos eventos: uno por el alta, uno por la actualización (P17).
	if n := cuentaOutbox(t, ctx, repo, v1.Id.String()); n != 2 {
		t.Fatalf("se esperaban 2 eventos en outbox, hubo %d", n)
	}
}

// TestListarPorLote: devuelve solo los ids pedidos, en su versión vigente (C4).
func TestListarPorLote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	cliente := uuid.New()
	nueva := func() catclient.Id {
		v, err := repo.Crear(ctx, catclient.VehiculoEntrada{
			Tipo: catclient.Moto, Estado: catclient.Activa, ClienteId: cliente,
		}, cliente)
		if err != nil {
			t.Fatalf("Crear: %v", err)
		}
		return v.Id
	}
	a, b := nueva(), nueva()
	_ = nueva() // un tercero que NO se pide

	pag, err := repo.ListarPorLote(ctx, []catclient.Id{a, b})
	if err != nil {
		t.Fatalf("ListarPorLote: %v", err)
	}
	if len(pag.Items) != 2 {
		t.Fatalf("se esperaban 2 vehículos en el lote, hubo %d", len(pag.Items))
	}
	if pag.CursorSiguiente != nil {
		t.Fatalf("el lote no se pagina: cursor_siguiente debía ser nil")
	}
	for _, v := range pag.Items {
		if v.Id != a && v.Id != b {
			t.Fatalf("el lote devolvió un id no pedido: %s", v.Id)
		}
	}
}
