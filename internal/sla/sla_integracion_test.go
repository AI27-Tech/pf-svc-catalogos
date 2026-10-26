package sla_test

import (
	"context"
	"io/fs"
	"sort"
	"testing"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/sla"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pruebas de integración del SLA (E5-T4). Cubren el criterio de aceptación
// central de la historia — `?a_fecha` devuelve el SLA que regía en esa fecha, no
// el actual — más versionado por vigencia (P8) y outbox en la misma tx (P17).
// Se saltan sin CATALOGOS_TEST_DB.

const (
	schema = "catalogos"
	role   = "svc_catalogos"
)

func setup(t *testing.T, ctx context.Context) (*sla.Repositorio, func()) {
	t.Helper()
	admin, closeAdmin := dbtest.AdminConn(t, ctx)

	limpiar := func() {
		dbtest.Exec(t, ctx, admin, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		dbtest.Exec(t, ctx, admin, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	}
	limpiar()

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
	repo := sla.NewRepositorio(pool)

	return repo, func() {
		closePool()
		limpiar()
		closeAdmin()
	}
}

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

// TestSlaVigenteAUnaFecha: el criterio de aceptación de E5-T4. Un SLA que cambió
// de versión devuelve, con `?a_fecha` en el rango de la v1, el compromiso que
// regía entonces — no el actual (P8). Prueba el valor con `descripcion` distinto
// entre versiones.
func TestSlaVigenteAUnaFecha(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	contrato := uuid.New()
	v1, err := repo.Crear(ctx, catclient.SlaEntrada{
		ContratoId:  contrato,
		Descripcion: str("respuesta en 30 min"),
	})
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	if v1.Version == nil || *v1.Version != 1 {
		t.Fatalf("se esperaba version 1, fue %v", v1.Version)
	}

	enVigenciaV1 := time.Now()
	time.Sleep(10 * time.Millisecond)

	// Nueva versión con un compromiso distinto.
	v2, err := repo.Actualizar(ctx, v1.Id, catclient.SlaCambios{Descripcion: str("respuesta en 15 min")}, time.Now())
	if err != nil {
		t.Fatalf("Actualizar: %v", err)
	}
	if v2.Version == nil || *v2.Version != 2 {
		t.Fatalf("se esperaba version 2, fue %v", v2.Version)
	}

	// Vigente = v2 (el compromiso actual).
	vigente, err := repo.Obtener(ctx, v1.Id, nil)
	if err != nil {
		t.Fatalf("Obtener vigente: %v", err)
	}
	if vigente.Descripcion == nil || *vigente.Descripcion != "respuesta en 15 min" {
		t.Fatalf("la vigente debía ser el compromiso v2, fue %v", vigente.Descripcion)
	}

	// A una fecha dentro de la vigencia de v1 ⇒ el compromiso v1 (criterio E5-T4).
	historico, err := repo.Obtener(ctx, v1.Id, &enVigenciaV1)
	if err != nil {
		t.Fatalf("Obtener a-fecha: %v", err)
	}
	if historico.Version == nil || *historico.Version != 1 {
		t.Fatalf("a-fecha en v1 debía devolver la versión 1, fue %v", historico.Version)
	}
	if historico.Descripcion == nil || *historico.Descripcion != "respuesta en 30 min" {
		t.Fatalf("a-fecha en v1 debía devolver el compromiso original, fue %v", historico.Descripcion)
	}
}

// TestSlaCrear_EmiteEventoEnLaMismaTx: crear un SLA emite catalogo.actualizado
// en la misma tx (P17).
func TestSlaCrear_EmiteEventoEnLaMismaTx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	s, err := repo.Crear(ctx, catclient.SlaEntrada{ContratoId: uuid.New(), Descripcion: str("x")})
	if err != nil {
		t.Fatalf("Crear: %v", err)
	}
	if n := cuentaOutbox(t, ctx, s.Id.String()); n != 1 {
		t.Fatalf("se esperaba 1 evento en outbox, hubo %d (P17)", n)
	}
}

// TestSlaObtener_NoEncontrado: un id inexistente devuelve ErrNoEncontrado.
func TestSlaObtener_NoEncontrado(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, teardown := setup(t, ctx)
	defer teardown()

	if _, err := repo.Obtener(ctx, uuid.New(), nil); err == nil {
		t.Fatalf("se esperaba ErrNoEncontrado, no hubo error")
	}
}
