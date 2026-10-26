package clientes_test

import (
	"context"
	"io/fs"
	"sort"
	"testing"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/clientes"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pruebas de integración de la escritura de contratos (E5-T4): verifican los
// criterios que solo tienen sentido contra Postgres real — versionado por
// vigencia (P8), lectura "a una fecha" y outbox en la misma transacción (P17).
// Se saltan sin CATALOGOS_TEST_DB.
//
// CATALOGOS_TEST_DB debe ser un DSN con privilegios para CREATE ROLE/SCHEMA.

const (
	schema = "catalogos"
	role   = "svc_catalogos"
)

func setup(t *testing.T, ctx context.Context) (*clientes.RepositorioContrato, func()) {
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
	repo := clientes.NewRepositorioContrato(pool)

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

func str(s string) *string { return &s }

// TestContratoCrear_EmiteEventoEnLaMismaTx: crear un contrato inserta la versión
// 1 Y emite catalogo.actualizado — ambos presentes tras el commit (P17).
func TestContratoCrear_EmiteEventoEnLaMismaTx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	cliente := uuid.New()
	ct, err := repo.Crear(ctx, catclient.ContratoEntrada{
		ClienteId:   cliente,
		Descripcion: str("contrato marco de seguridad"),
	})
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	if ct.Version == nil || *ct.Version != 1 {
		t.Fatalf("se esperaba version 1, fue %v", ct.Version)
	}
	if ct.ClienteId != cliente {
		t.Fatalf("el contrato debía quedar ligado al cliente %s, fue %s", cliente, ct.ClienteId)
	}
	if n := cuentaOutbox(t, ctx, ct.Id.String()); n != 1 {
		t.Fatalf("se esperaba 1 evento en outbox, hubo %d (P17)", n)
	}
}

// TestContratoActualizar_CreaNuevaVersionSinSobrescribir: modificar crea la
// versión 2; la versión 1 sigue consultable "a una fecha" anterior (P8), y hay
// un segundo evento (P17).
func TestContratoActualizar_CreaNuevaVersionSinSobrescribir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	cliente := uuid.New()
	v1, err := repo.Crear(ctx, catclient.ContratoEntrada{
		ClienteId: cliente, Descripcion: str("v1"),
	})
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	antesDelCambio := time.Now()
	time.Sleep(10 * time.Millisecond)

	v2, err := repo.Actualizar(ctx, v1.Id, catclient.ContratoCambios{Descripcion: str("v2")}, time.Now())
	if err != nil {
		t.Fatalf("Actualizar: %v", err)
	}
	if v2.Version == nil || *v2.Version != 2 {
		t.Fatalf("se esperaba version 2, fue %v", v2.Version)
	}

	// La versión vigente ahora es la 2.
	vigente, err := repo.Obtener(ctx, v1.Id, nil)
	if err != nil {
		t.Fatalf("Obtener vigente: %v", err)
	}
	if vigente.Version == nil || *vigente.Version != 2 {
		t.Fatalf("la vigente debía ser la versión 2, fue %v", vigente.Version)
	}

	// Leer "a una fecha" ANTES del cambio devuelve la versión 1 (P8).
	historico, err := repo.Obtener(ctx, v1.Id, &antesDelCambio)
	if err != nil {
		t.Fatalf("Obtener a-fecha: %v", err)
	}
	if historico.Version == nil || *historico.Version != 1 {
		t.Fatalf("a-fecha anterior debía devolver la versión 1, fue %v", historico.Version)
	}

	// Dos eventos: uno por el alta, uno por la actualización (P17).
	if n := cuentaOutbox(t, ctx, v1.Id.String()); n != 2 {
		t.Fatalf("se esperaban 2 eventos en outbox, hubo %d", n)
	}
}

// TestContratoObtener_NoEncontrado: un id inexistente devuelve
// ErrContratoNoEncontrado (que el adaptador traduce a 404).
func TestContratoObtener_NoEncontrado(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	_, err := repo.Obtener(ctx, uuid.New(), nil)
	if err == nil {
		t.Fatalf("se esperaba ErrContratoNoEncontrado, no hubo error")
	}
}
