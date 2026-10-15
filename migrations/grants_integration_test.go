package migrations_test

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-pkg-platform-go/inbox"
	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	schema = "catalogos"
	role   = "svc_catalogos"
)

// TestGrants verifica el invariante I1 (ADR 17) para catalogos al nivel que
// cierra E0-T5: aislamiento por esquema y privilegio mínimo por tabla. Aplica
// las migraciones REALES (0001 estructura + 0002 grants, en orden de nombre)
// sobre una base efímera y comprueba, con escrituras reales ejecutadas como
// svc_catalogos, qué permite y qué rechaza Postgres.
//
// Prueba comportamiento observable: publica en el outbox con el publicador de
// platform-go, registra en el inbox con inbox.Registrar, marca como lo hace el
// relay, y afirma el error literal de Postgres en lo denegado.
//
// Se salta si CATALOGOS_TEST_DB no está definido (CI sin Postgres).
func TestGrants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()

	// Esquema y rol ajenos, efímeros: representan "otro módulo" sin depender de
	// las migraciones de otro repo (la prueba cruzada real vive fuera, ver E0-T5).
	suffix := fmt.Sprintf("_e0t5_%d", time.Now().UnixNano())
	foreignSchema := "ajeno" + suffix
	foreignRole := "rol_ajeno" + suffix

	cleanup := func() {
		for _, s := range []string{schema, foreignSchema} {
			dbtest.Exec(t, ctx, admin, "DROP SCHEMA IF EXISTS "+pgx.Identifier{s}.Sanitize()+" CASCADE")
		}
		for _, r := range []string{role, foreignRole} {
			dbtest.Exec(t, ctx, admin, "DROP ROLE IF EXISTS "+pgx.Identifier{r}.Sanitize())
		}
	}
	cleanup()
	defer cleanup()

	applyMigrations(t, ctx, admin)

	// "Otro módulo": un esquema con una tabla, y un rol sin ningún GRANT.
	dbtest.Exec(t, ctx, admin, fmt.Sprintf("CREATE SCHEMA %s", foreignSchema))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf("CREATE TABLE %s.dato (id int)", foreignSchema))
	dbtest.Exec(t, ctx, admin, fmt.Sprintf("CREATE ROLE %s NOLOGIN", foreignRole))
	// Una tabla creada DESPUÉS de 0002 (como haría una 0003) no debe heredar
	// privilegios por ALTER DEFAULT PRIVILEGES.
	dbtest.Exec(t, ctx, admin, "CREATE TABLE catalogos.tabla_futura (id int)")

	pool, closePool := dbtest.PoolAs(t, ctx, role, schema)
	defer closePool()

	// --- Lo que el caso de uso necesita: debe funcionar. ---
	clienteID := "11111111-1111-1111-1111-111111111111"
	mustExec(t, ctx, pool,
		fmt.Sprintf(`INSERT INTO catalogos.cliente (id, version, nombre, estado) VALUES ('%s', 1, 'n', 'activo')`, clienteID),
		"svc_catalogos debe poder insertar una versión de cliente")
	mustExec(t, ctx, pool,
		fmt.Sprintf(`UPDATE catalogos.cliente SET vigente_hasta = now() WHERE id = '%s' AND version = 1`, clienteID),
		"svc_catalogos debe poder cerrar la vigencia de una versión")
	custodioID := "44444444-4444-4444-4444-444444444444"
	mustExec(t, ctx, pool,
		fmt.Sprintf(`INSERT INTO catalogos.custodio (id, version, nombre, estado) VALUES ('%s', 1, 'n', 'activo')`, custodioID),
		"svc_catalogos debe poder insertar una versión de custodio")
	mustExec(t, ctx, pool,
		fmt.Sprintf(`UPDATE catalogos.custodio SET vigente_hasta = now() WHERE id = '%s' AND version = 1`, custodioID),
		"svc_catalogos debe poder cerrar la vigencia de un custodio")

	// Vehículo (0003/0004): mismo patrón de versionado por vigencia (P8).
	vehiculoID := "33333333-3333-3333-3333-333333333333"
	mustExec(t, ctx, pool,
		fmt.Sprintf(`INSERT INTO catalogos.vehiculo (id, version, tipo, estado, cliente_id) VALUES ('%s', 1, 'coche', 'activa', '%s')`, vehiculoID, clienteID),
		"svc_catalogos debe poder insertar una versión de vehículo")
	mustExec(t, ctx, pool,
		fmt.Sprintf(`UPDATE catalogos.vehiculo SET vigente_hasta = now() WHERE id = '%s' AND version = 1`, vehiculoID),
		"svc_catalogos debe poder cerrar la vigencia de un vehículo")
	mustExec(t, ctx, pool,
		`INSERT INTO catalogos.vehiculo_documento (id, version, vehiculo_id, clase) VALUES (gen_random_uuid(), 1, '33333333-3333-3333-3333-333333333333', 'poliza')`,
		"svc_catalogos debe poder insertar un documento de vehículo")

	// Valor de catálogo de tipo (0006/0007): mismo patrón de versionado (P8).
	valorID := "55555555-5555-5555-5555-555555555555"
	mustExec(t, ctx, pool,
		fmt.Sprintf(`INSERT INTO catalogos.valor_catalogo (id, version, tipo, codigo, etiqueta, estado) VALUES ('%s', 1, 'lineas_transporte', 'TRANSPORTES-SA', 'Transportes S.A.', 'activo')`, valorID),
		"svc_catalogos debe poder insertar un valor de catálogo de tipo")
	mustExec(t, ctx, pool,
		fmt.Sprintf(`UPDATE catalogos.valor_catalogo SET vigente_hasta = now() WHERE id = '%s' AND version = 1`, valorID),
		"svc_catalogos debe poder cerrar la vigencia de un valor de catálogo")

	// Publicar catalogo.actualizado con el publicador real, en una tx.
	inTx(t, ctx, pool, "svc_catalogos debe poder publicar en su outbox", func(tx pgx.Tx) error {
		return outbox.NewPublisher().Publish(ctx, tx, outbox.Event{
			Tipo: "catalogo.actualizado", AgregadoID: clienteID, Payload: map[string]any{},
		})
	})
	// El relay marca la fila publicada con esta sentencia (platform-go/outbox).
	mustExec(t, ctx, pool,
		`UPDATE outbox SET publicado = true, publicado_en = now() WHERE id = (SELECT min(id) FROM outbox)`,
		"svc_catalogos debe poder marcar el outbox como publicado")

	// Registrar un sesion.revocada consumido, con el inbox real.
	inTx(t, ctx, pool, "svc_catalogos debe poder registrar en su inbox", func(tx pgx.Tx) error {
		return inbox.Registrar(ctx, tx, "22222222-2222-2222-2222-222222222222", "sesion.revocada", "catalogos")
	})

	// --- Lo que el caso de uso NO necesita: la base lo rechaza. ---
	denied := []struct{ sql, wantErr, msg string }{
		{`DELETE FROM catalogos.cliente`, "permission denied for table cliente",
			"no se borra en firme una versión (P8)"},
		{`UPDATE catalogos.cliente SET nombre = 'otro'`, "permission denied for table cliente",
			"no se reescribe una versión: solo se cierra su vigencia (P8)"},
		{`DELETE FROM catalogos.contrato`, "permission denied for table contrato", "no se borra un contrato"},
		{`DELETE FROM catalogos.sla`, "permission denied for table sla", "no se borra un SLA"},
		{`DELETE FROM catalogos.custodio`, "permission denied for table custodio", "no se borra un custodio"},
		{`UPDATE catalogos.custodio SET nombre = 'otro'`, "permission denied for table custodio",
			"no se reescribe una versión de custodio: solo se cierra su vigencia (P8)"},
		{`DELETE FROM catalogos.vehiculo`, "permission denied for table vehiculo",
			"no se borra en firme una versión de vehículo (P8)"},
		{`UPDATE catalogos.vehiculo SET placas = 'x'`, "permission denied for table vehiculo",
			"no se reescribe una versión de vehículo: solo se cierra su vigencia (P8)"},
		{`DELETE FROM catalogos.vehiculo_documento`, "permission denied for table vehiculo_documento",
			"no se borra en firme un documento de vehículo (P8)"},
		{`UPDATE catalogos.vehiculo_documento SET clase = 'x'`, "permission denied for table vehiculo_documento",
			"no se reescribe un documento: solo se cierra su vigencia (P8)"},
		{`DELETE FROM catalogos.valor_catalogo`, "permission denied for table valor_catalogo",
			"no se borra en firme un valor de catálogo (P8)"},
		{`UPDATE catalogos.valor_catalogo SET etiqueta = 'otra'`, "permission denied for table valor_catalogo",
			"no se reescribe una versión de valor: solo se cierra su vigencia (P8)"},
		{`DELETE FROM catalogos.outbox`, "permission denied for table outbox", "no se borra el outbox"},
		{`UPDATE catalogos.inbox_evento SET tipo = 'x'`, "permission denied for table inbox_evento",
			"el inbox no se reescribe"},
		{`DELETE FROM catalogos.inbox_evento`, "permission denied for table inbox_evento", "el inbox no se borra"},
		{`SELECT 1 FROM catalogos.tabla_futura`, "permission denied for table tabla_futura",
			"una tabla futura no hereda privilegios por defecto"},
		{fmt.Sprintf(`SELECT 1 FROM %s.dato`, foreignSchema), "permission denied for schema " + foreignSchema,
			"svc_catalogos no alcanza el esquema de otro módulo (I1)"},
	}
	for _, d := range denied {
		mustDeny(t, ctx, pool, d.sql, d.wantErr, d.msg)
	}

	// --- Ningún otro rol alcanza catalogos: no hay GRANT a PUBLIC ni a terceros. ---
	foreignPool, closeForeign := dbtest.PoolAs(t, ctx, foreignRole, "")
	defer closeForeign()
	mustDeny(t, ctx, foreignPool, `SELECT 1 FROM catalogos.cliente`, "permission denied for schema catalogos",
		"un rol ajeno no alcanza el esquema catalogos (I1)")

	// --- svc_catalogos tiene USAGE sobre un solo esquema de módulo: el suyo. ---
	assertUsageOnlyOn(t, ctx, admin, role, schema)
}

// applyMigrations aplica, como admin, todos los .sql del esquema en orden de
// nombre: 0001 (estructura) y luego 0002 (grants).
func applyMigrations(t *testing.T, ctx context.Context, admin *pgx.Conn) {
	t.Helper()
	files, err := fs.Glob(migrations.FS, schema+"/*.sql")
	if err != nil {
		t.Fatalf("listar migraciones: %v", err)
	}
	sort.Strings(files)
	if len(files) < 2 {
		t.Fatalf("se esperaban al menos 0001 y 0002, hay: %v", files)
	}
	for _, f := range files {
		sql, err := migrations.FS.ReadFile(f)
		if err != nil {
			t.Fatalf("leer %s: %v", f, err)
		}
		if _, err := admin.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("aplicar %s: %v", f, err)
		}
	}
}

// assertUsageOnlyOn afirma que el rol tiene USAGE solo sobre wantSchema entre
// los esquemas no de sistema. Se excluye `public`: su USAGE llega por el
// pseudo-rol PUBLIC (default de Postgres), es política de la base y no del
// módulo; queda como pregunta abierta de E0-T5.
func assertUsageOnlyOn(t *testing.T, ctx context.Context, admin *pgx.Conn, roleName, wantSchema string) {
	t.Helper()
	rows, err := admin.Query(ctx, `
		SELECT n.nspname FROM pg_namespace n
		 WHERE has_schema_privilege($1, n.oid, 'USAGE')
		   AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
		   AND n.nspname <> 'public'
		 ORDER BY 1`, roleName)
	if err != nil {
		t.Fatalf("consultar USAGE de %s: %v", roleName, err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("leer USAGE de %s: %v", roleName, err)
	}
	if len(got) != 1 || got[0] != wantSchema {
		t.Fatalf("%s debe tener USAGE solo sobre [%s], tiene: %v", roleName, wantSchema, got)
	}
}

func inTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, msg string, fn func(pgx.Tx) error) {
	t.Helper()
	if err := pgx.BeginFunc(ctx, pool, fn); err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql, msg string) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func mustDeny(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql, wantErr, msg string) {
	t.Helper()
	_, err := pool.Exec(ctx, sql)
	if err == nil {
		t.Fatalf("%s: la operación tuvo éxito pero debía ser denegada", msg)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("%s: se esperaba un error con %q, se obtuvo: %v", msg, wantErr, err)
	}
}
