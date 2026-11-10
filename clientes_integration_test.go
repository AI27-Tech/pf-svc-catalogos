package catalogos_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	platformerrors "github.com/AI27-Tech/pf-pkg-platform-go/errors"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pruebas de la organización cliente de tracer.40 (E8-T19, DEC-139 #2,
// DEC-144 #2): alta idempotente por id (PUT), edición y desactivación (PATCH)
// y lectura con datos de contacto. Van por HTTP y por fachada, contra Postgres.

const rutaCliente = "/api/v1/clientes/00000000-0000-0000-0000-0000000000e1"

var idCliente = uuid.MustParse("00000000-0000-0000-0000-0000000000e1")

// pedir sirve una petición por el mux del módulo y devuelve status y cuerpo.
func pedir(t *testing.T, mux *http.ServeMux, metodo, ruta, cuerpo string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(metodo, ruta, strings.NewReader(cuerpo)))
	return rec.Code, rec.Body.String()
}

// eventosCliente cuenta los catalogo.actualizado de entidad cliente para id, y
// devuelve la versión del último.
func eventosCliente(t *testing.T, ctx context.Context, admin *pgx.Conn, id uuid.UUID) (n, ultimaVersion int) {
	t.Helper()
	err := admin.QueryRow(ctx, `
		SELECT count(*), coalesce(max(version), 0) FROM catalogos.outbox
		 WHERE tipo = 'catalogo.actualizado' AND agregado_id = $1
		   AND payload->>'entidad' = 'cliente' AND payload->>'entidad_id' = $1`,
		id.String()).Scan(&n, &ultimaVersion)
	if err != nil {
		t.Fatalf("contar eventos de %s: %v", id, err)
	}
	return n, ultimaVersion
}

// versionesCliente cuenta las filas de cliente de id (todas sus versiones).
func versionesCliente(t *testing.T, ctx context.Context, admin *pgx.Conn, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM catalogos.cliente WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("contar versiones de %s: %v", id, err)
	}
	return n
}

// TestCrearCliente_AltaIdempotente: el primer PUT crea el cliente activo con su
// contacto (201) y emite un catalogo.actualizado; el segundo, con otros datos,
// responde 200 con el cliente guardado sin modificarlo y sin emitir otro evento.
func TestCrearCliente_AltaIdempotente(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	mux := http.NewServeMux()
	m.Routes(mux)

	status, cuerpo := pedir(t, mux, http.MethodPut, rutaCliente,
		`{"nombre":"Transportes Norte","correo":"ops@norte.mx","telefono":"5550001111"}`)
	if status != http.StatusCreated {
		t.Fatalf("primer PUT: status %d cuerpo %s; quiere 201", status, cuerpo)
	}
	var cl catclient.Cliente
	if err := json.Unmarshal([]byte(cuerpo), &cl); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if cl.Id != idCliente || cl.Nombre != "Transportes Norte" || cl.Estado != catclient.ClienteEstadoActivo ||
		cl.Correo == nil || *cl.Correo != "ops@norte.mx" || cl.Telefono == nil || *cl.Telefono != "5550001111" {
		t.Fatalf("primer PUT: got %+v; quiere Transportes Norte activo con su contacto", cl)
	}
	if n, v := eventosCliente(t, ctx, admin, idCliente); n != 1 || v != 1 {
		t.Fatalf("tras el alta: %d eventos (versión %d); quiere 1 de versión 1", n, v)
	}

	status, cuerpo = pedir(t, mux, http.MethodPut, rutaCliente,
		`{"nombre":"Otro Nombre","correo":"otro@x.mx","telefono":"1"}`)
	if status != http.StatusOK || !strings.Contains(cuerpo, `"nombre":"Transportes Norte"`) || !strings.Contains(cuerpo, `"correo":"ops@norte.mx"`) {
		t.Fatalf("PUT repetido: status %d cuerpo %s; quiere 200 con el cliente original", status, cuerpo)
	}
	if n, _ := eventosCliente(t, ctx, admin, idCliente); n != 1 {
		t.Errorf("PUT repetido emitió evento: %d en total; quiere 1", n)
	}
	if n := versionesCliente(t, ctx, admin, idCliente); n != 1 {
		t.Errorf("PUT repetido: %d versiones; quiere 1", n)
	}
}

// TestCrearCliente_Carrera: dos altas simultáneas con el mismo id terminan en un
// solo cliente y un solo evento; exactamente una reporta creado.
func TestCrearCliente_Carrera(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()

	const n = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		creados int
		errs    []error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, creado, err := m.Comandos().CrearCliente(ctx, idCliente, catclient.ClienteEntrada{
				Nombre: "Fletes Baja", Correo: "a@b", Telefono: "1",
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			}
			if creado {
				creados++
			}
		}()
	}
	wg.Wait()
	if len(errs) != 0 || creados != 1 {
		t.Fatalf("carrera: %d creados, errores %v; quiere 1 creado y sin errores", creados, errs)
	}
	if v := versionesCliente(t, ctx, admin, idCliente); v != 1 {
		t.Errorf("carrera: %d versiones; quiere 1", v)
	}
	if e, _ := eventosCliente(t, ctx, admin, idCliente); e != 1 {
		t.Errorf("carrera: %d eventos; quiere 1", e)
	}
}

// TestCrearCliente_Validacion: nombre, correo y teléfono son obligatorios y no
// vacíos, y el correo debe contener `@` (DEC-139 #2). Todo rechazo es 400 y no
// deja fila.
func TestCrearCliente_Validacion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	mux := http.NewServeMux()
	m.Routes(mux)

	for _, cuerpo := range []string{
		`{"nombre":"N","correo":"sin-arroba.mx","telefono":"1"}`,
		`{"nombre":"","correo":"a@b","telefono":"1"}`,
		`{"nombre":"N","correo":"","telefono":"1"}`,
		`{"nombre":"N","correo":"a@b","telefono":""}`,
		`{"correo":"a@b","telefono":"1"}`,
		`{}`,
	} {
		if status, resp := pedir(t, mux, http.MethodPut, rutaCliente, cuerpo); status != http.StatusBadRequest {
			t.Errorf("PUT %s: status %d cuerpo %s; quiere 400", cuerpo, status, resp)
		}
	}
	if status, _ := pedir(t, mux, http.MethodPut, "/api/v1/clientes/no-es-id", `{"nombre":"N","correo":"a@b","telefono":"1"}`); status != http.StatusBadRequest {
		t.Errorf("PUT id inválido: status %d; quiere 400", status)
	}
	_, _, err := m.Comandos().CrearCliente(ctx, idCliente, catclient.ClienteEntrada{Nombre: "N", Correo: "sin-arroba", Telefono: "1"})
	if got := platformerrors.AsCategory(err); got != platformerrors.CategoryValidacion {
		t.Errorf("fachada, correo sin @: categoría %v (err %v); quiere Validacion", got, err)
	}
	if n := versionesCliente(t, ctx, admin, idCliente); n != 0 {
		t.Errorf("tras rechazos: %d filas; quiere 0", n)
	}
}

// TestActualizarCliente_VersionaYEmite: un PATCH crea la versión 2 (la 1 queda
// cerrada, P8), conserva lo no enviado y emite catalogo.actualizado de versión 2.
// Un cambio inválido es 400 y un cliente inexistente es 404.
func TestActualizarCliente_VersionaYEmite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	mux := http.NewServeMux()
	m.Routes(mux)
	if _, _, err := m.Comandos().CrearCliente(ctx, idCliente, catclient.ClienteEntrada{Nombre: "Transportes Norte", Correo: "ops@norte.mx", Telefono: "1"}); err != nil {
		t.Fatalf("CrearCliente: %v", err)
	}

	status, cuerpo := pedir(t, mux, http.MethodPatch, rutaCliente, `{"telefono":"5559998888"}`)
	if status != http.StatusOK || !strings.Contains(cuerpo, `"telefono":"5559998888"`) || !strings.Contains(cuerpo, `"correo":"ops@norte.mx"`) || !strings.Contains(cuerpo, `"nombre":"Transportes Norte"`) {
		t.Fatalf("PATCH: status %d cuerpo %s; quiere 200 con teléfono nuevo y lo demás igual", status, cuerpo)
	}
	var cerradas, vigentes int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE version = 1 AND vigente_hasta IS NOT NULL AND telefono = '1'),
		       count(*) FILTER (WHERE version = 2 AND vigente_hasta IS NULL)
		  FROM catalogos.cliente WHERE id = $1`, idCliente).Scan(&cerradas, &vigentes); err != nil {
		t.Fatalf("leer versiones: %v", err)
	}
	if cerradas != 1 || vigentes != 1 {
		t.Errorf("versiones: %d cerradas v1, %d vigentes v2; quiere 1 y 1", cerradas, vigentes)
	}
	if n, v := eventosCliente(t, ctx, admin, idCliente); n != 2 || v != 2 {
		t.Errorf("eventos: %d (última versión %d); quiere 2 con la última de versión 2", n, v)
	}

	for _, malo := range []string{`{"correo":"sin-arroba"}`, `{"nombre":""}`, `{"telefono":""}`, `{"estado":"borrado"}`} {
		if status, resp := pedir(t, mux, http.MethodPatch, rutaCliente, malo); status != http.StatusBadRequest {
			t.Errorf("PATCH %s: status %d cuerpo %s; quiere 400", malo, status, resp)
		}
	}
	if status, _ := pedir(t, mux, http.MethodPatch, "/api/v1/clientes/00000000-0000-0000-0000-0000000000e9", `{"nombre":"X"}`); status != http.StatusNotFound {
		t.Errorf("PATCH inexistente: status %d; quiere 404", status)
	}
	if n := versionesCliente(t, ctx, admin, idCliente); n != 2 {
		t.Errorf("tras rechazos: %d versiones; quiere 2", n)
	}
}

// TestActualizarCliente_Desactivar: estado inactivo saca al cliente del selector
// (DEC-101 #1) y la lectura por id lo sigue devolviendo, ya inactivo.
func TestActualizarCliente_Desactivar(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	if _, _, err := m.Comandos().CrearCliente(ctx, idCliente, catclient.ClienteEntrada{Nombre: "N", Correo: "a@b", Telefono: "1"}); err != nil {
		t.Fatalf("CrearCliente: %v", err)
	}
	inactivo := catclient.ClienteCambiosEstadoInactivo
	cl, err := m.Comandos().ActualizarCliente(ctx, idCliente, catclient.ClienteCambios{Estado: &inactivo})
	if err != nil || cl.Estado != catclient.ClienteEstadoInactivo {
		t.Fatalf("desactivar: got %+v err %v; quiere inactivo", cl, err)
	}
	pag, err := m.Consultas().ListarClientes(ctx, nil)
	if err != nil || len(pag.Items) != 0 {
		t.Errorf("selector tras desactivar: %+v err %v; quiere vacío", pag, err)
	}
	cl, err = m.Consultas().ObtenerCliente(ctx, idCliente)
	if err != nil || cl.Estado != catclient.ClienteEstadoInactivo {
		t.Errorf("lectura por id tras desactivar: %+v err %v; quiere inactivo", cl, err)
	}
}

// TestLeerCliente_ConContacto: la lectura por id y el listado devuelven correo y
// teléfono cuando existen; un cliente previo a tracer.40 los omite.
func TestLeerCliente_ConContacto(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	dbtest.Exec(t, ctx, admin, `INSERT INTO catalogos.cliente (id, version, nombre, estado) VALUES
		('00000000-0000-0000-0000-0000000000e0', 1, 'Previo', 'activo')`)
	if _, _, err := m.Comandos().CrearCliente(ctx, idCliente, catclient.ClienteEntrada{Nombre: "Nuevo", Correo: "c@d.mx", Telefono: "5551234567"}); err != nil {
		t.Fatalf("CrearCliente: %v", err)
	}

	cl, err := m.Consultas().ObtenerCliente(ctx, idCliente)
	if err != nil || cl.Correo == nil || *cl.Correo != "c@d.mx" || cl.Telefono == nil || *cl.Telefono != "5551234567" {
		t.Fatalf("ObtenerCliente: %+v err %v; quiere c@d.mx y 5551234567", cl, err)
	}
	pag, err := m.Consultas().ListarClientes(ctx, nil)
	if err != nil || len(pag.Items) != 2 {
		t.Fatalf("ListarClientes: %+v err %v; quiere 2", pag, err)
	}
	previo, nuevo := pag.Items[0], pag.Items[1]
	if previo.Correo != nil || previo.Telefono != nil {
		t.Errorf("cliente previo: correo %v teléfono %v; quiere ambos nil", previo.Correo, previo.Telefono)
	}
	if nuevo.Correo == nil || *nuevo.Correo != "c@d.mx" || nuevo.Telefono == nil || *nuevo.Telefono != "5551234567" {
		t.Errorf("cliente nuevo en el listado: %+v; quiere su contacto", nuevo)
	}

	mux := http.NewServeMux()
	m.Routes(mux)
	if status, cuerpo := pedir(t, mux, http.MethodGet, "/api/v1/clientes/00000000-0000-0000-0000-0000000000e0", ""); status != http.StatusOK || strings.Contains(cuerpo, "correo") {
		t.Errorf("GET previo: status %d cuerpo %s; quiere 200 sin correo", status, cuerpo)
	}
}
