package clientes_test

import (
	"context"
	"io/fs"
	"sort"
	"testing"
	"time"

	"github.com/AI27-Tech/pf-svc-catalogos/internal/clientes"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pruebas de integración del listado de clientes (E8-T11): verifican lo que solo
// tiene sentido contra Postgres real — solo clientes activos (DEC-101 #1), la
// versión vigente, y la paginación por cursor. Se saltan sin CATALOGOS_TEST_DB.

func setupClientes(t *testing.T, ctx context.Context) (*clientes.RepositorioCliente, *pgxpool.Pool, func()) {
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
	repo := clientes.NewRepositorioCliente(pool)

	return repo, pool, func() {
		closePool()
		limpiar()
		closeAdmin()
	}
}

// insertarCliente da de alta un cliente en su versión vigente con el estado dado.
// No hay caso de uso de alta (brecha C7); el test inserta directo con el pool.
func insertarCliente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, nombre, estado string) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO catalogos.cliente (id, version, nombre, estado) VALUES ($1, 1, $2, $3)`,
		id, nombre, estado)
	if err != nil {
		t.Fatalf("insertar cliente %s: %v", nombre, err)
	}
}

// TestListarClientes_SoloActivos: el listado excluye los clientes inactivos
// (DEC-101 #1); un cliente inactivo no es asignable a un viaje nuevo.
func TestListarClientes_SoloActivos(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, pool, teardown := setupClientes(t, ctx)
	defer teardown()

	activo := uuid.New()
	inactivo := uuid.New()
	insertarCliente(t, ctx, pool, activo, "Cliente Activo", "activo")
	insertarCliente(t, ctx, pool, inactivo, "Cliente Inactivo", "inactivo")

	pag, err := repo.Listar(ctx, nil)
	if err != nil {
		t.Fatalf("Listar: %v", err)
	}
	if len(pag.Items) != 1 {
		t.Fatalf("se esperaba 1 cliente activo, hubo %d", len(pag.Items))
	}
	if pag.Items[0].Id != activo {
		t.Fatalf("el listado debía traer el cliente activo %s, trajo %s", activo, pag.Items[0].Id)
	}
	if string(pag.Items[0].Estado) != "activo" {
		t.Fatalf("estado esperado activo, fue %q", pag.Items[0].Estado)
	}
}

// TestListarClientes_PaginaPorCursor: con más clientes que el tamaño de página,
// la primera página trae `pagina` items y un cursor; la segunda trae el resto y
// sin cursor. Verifica que no se repiten ni se pierden clientes.
func TestListarClientes_PaginaPorCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	repo, pool, teardown := setupClientes(t, ctx)
	defer teardown()

	// 51 activos fuerza una segunda página (tamaño de página = 50).
	const total = 51
	vistos := map[uuid.UUID]bool{}
	for i := 0; i < total; i++ {
		id := uuid.New()
		insertarCliente(t, ctx, pool, id, "Cliente", "activo")
	}

	pag1, err := repo.Listar(ctx, nil)
	if err != nil {
		t.Fatalf("Listar p1: %v", err)
	}
	if len(pag1.Items) != 50 {
		t.Fatalf("primera página: se esperaban 50, hubo %d", len(pag1.Items))
	}
	if pag1.CursorSiguiente == nil {
		t.Fatalf("primera página debía traer cursor siguiente")
	}
	for _, c := range pag1.Items {
		vistos[c.Id] = true
	}

	cursor := *pag1.CursorSiguiente
	pag2, err := repo.Listar(ctx, &cursor)
	if err != nil {
		t.Fatalf("Listar p2: %v", err)
	}
	if len(pag2.Items) != 1 {
		t.Fatalf("segunda página: se esperaba 1, hubo %d", len(pag2.Items))
	}
	if pag2.CursorSiguiente != nil {
		t.Fatalf("segunda página no debía traer cursor siguiente")
	}
	for _, c := range pag2.Items {
		if vistos[c.Id] {
			t.Fatalf("cliente %s repetido entre páginas", c.Id)
		}
		vistos[c.Id] = true
	}
	if len(vistos) != total {
		t.Fatalf("se esperaban %d clientes únicos entre páginas, hubo %d", total, len(vistos))
	}
}

// TestListarClientes_Vacio: sin clientes, la página es vacía (Items no nil) y sin
// cursor.
func TestListarClientes_Vacio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, _, teardown := setupClientes(t, ctx)
	defer teardown()

	pag, err := repo.Listar(ctx, nil)
	if err != nil {
		t.Fatalf("Listar: %v", err)
	}
	if pag.Items == nil {
		t.Fatalf("Items debía ser lista vacía, no nil")
	}
	if len(pag.Items) != 0 {
		t.Fatalf("se esperaba página vacía, hubo %d", len(pag.Items))
	}
	if pag.CursorSiguiente != nil {
		t.Fatalf("página vacía no debía traer cursor")
	}
}
