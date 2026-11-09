package catalogos_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-pkg-platform-go/bus"
	platformerrors "github.com/AI27-Tech/pf-pkg-platform-go/errors"
	platformpgx "github.com/AI27-Tech/pf-pkg-platform-go/pgxpool"
	catalogos "github.com/AI27-Tech/pf-svc-catalogos"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/dbtest"
	"github.com/AI27-Tech/pf-svc-catalogos/migrations"
	"github.com/google/uuid"
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
	return moduloConDeps(t, ctx, catalogos.Deps{})
}

func moduloConDeps(t *testing.T, ctx context.Context, d catalogos.Deps) *catalogos.Module {
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
	m, err := catalogos.New(catalogos.Pools{Catalogos: &platformpgx.Pool{Pool: pool, Role: role}}, d)
	if err != nil {
		t.Fatalf("catalogos.New: %v", err)
	}
	return m
}

// TestConsultasListarGps: el selector de la ficha recibe solo los dispositivos
// activos, en orden de id, igual por fachada y por GET /api/v1/gps; un cursor
// que no es id es Validacion (400) por ambos caminos (DEC-104 #5).
func TestConsultasListarGps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	dbtest.Exec(t, ctx, admin, `INSERT INTO catalogos.gps (id, identificador, activo) VALUES
		('00000000-0000-0000-0000-0000000000d2', 'MOVIL ST7916', true),
		('00000000-0000-0000-0000-0000000000d1', 'MOVIL ST1001', true),
		('00000000-0000-0000-0000-0000000000d3', 'MOVIL RETIRADO', false)`)

	pag, err := m.Consultas().ListarGps(ctx, nil)
	if err != nil {
		t.Fatalf("ListarGps: %v", err)
	}
	if len(pag.Items) != 2 || pag.CursorSiguiente != nil ||
		pag.Items[0].Id.String() != "00000000-0000-0000-0000-0000000000d1" || pag.Items[0].Identificador != "MOVIL ST1001" || !pag.Items[0].Activo ||
		pag.Items[1].Identificador != "MOVIL ST7916" {
		t.Fatalf("got %+v, want ST1001 y ST7916 activos, sin cursor", pag)
	}

	mux := http.NewServeMux()
	m.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/gps", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "RETIRADO") || !strings.Contains(rec.Body.String(), `"identificador":"MOVIL ST7916"`) {
		t.Errorf("HTTP: status %d cuerpo %s; quiere 200 sin el inactivo", rec.Code, rec.Body)
	}

	malo := "no-es-id"
	if _, err := m.Consultas().ListarGps(ctx, &malo); platformerrors.AsCategory(err) != platformerrors.CategoryValidacion {
		t.Errorf("cursor inválido: err %v, quiere Validacion", err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/gps?cursor=no-es-id", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("HTTP cursor inválido: status %d, quiere 400", rec.Code)
	}
}

// TestConsultasObtenerCliente: el detalle de emergencia de Reacción compone
// `cliente_nombre` con obtenerCliente. Devuelve la versión vigente, también de un
// cliente inactivo (sigue siendo el cliente de su viaje), igual por fachada y por
// GET /api/v1/clientes/{id}; un id inexistente es NoEncontrado (404) y un id que
// no es uuid es 400.
func TestConsultasObtenerCliente(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	dbtest.Exec(t, ctx, admin, `INSERT INTO catalogos.cliente (id, version, nombre, estado, vigente_hasta) VALUES
		('00000000-0000-0000-0000-0000000000c1', 1, 'Transportes Norte Viejo', 'activo', now())`)
	dbtest.Exec(t, ctx, admin, `INSERT INTO catalogos.cliente (id, version, nombre, estado) VALUES
		('00000000-0000-0000-0000-0000000000c1', 2, 'Transportes Norte', 'activo'),
		('00000000-0000-0000-0000-0000000000c2', 1, 'Fletes Baja', 'inactivo')`)
	dbtest.Exec(t, ctx, admin, `INSERT INTO catalogos.contrato (id, version, cliente_id, vigente_hasta) VALUES
		('00000000-0000-0000-0000-0000000000a0', 1, '00000000-0000-0000-0000-0000000000c1', now())`)
	dbtest.Exec(t, ctx, admin, `INSERT INTO catalogos.contrato (id, version, cliente_id) VALUES
		('00000000-0000-0000-0000-0000000000a1', 1, '00000000-0000-0000-0000-0000000000c1')`)

	cl, err := m.Consultas().ObtenerCliente(ctx, uuid.MustParse("00000000-0000-0000-0000-0000000000c1"))
	if err != nil {
		t.Fatalf("ObtenerCliente(c1): %v", err)
	}
	if cl.Nombre != "Transportes Norte" || cl.Estado != "activo" || cl.ContratoVigenteId == nil || cl.ContratoVigenteId.String() != "00000000-0000-0000-0000-0000000000a1" {
		t.Fatalf("c1: got %+v, want Transportes Norte activo con contrato vigente a1", cl)
	}
	cl, err = m.Consultas().ObtenerCliente(ctx, uuid.MustParse("00000000-0000-0000-0000-0000000000c2"))
	if err != nil || cl.Nombre != "Fletes Baja" || cl.Estado != "inactivo" || cl.ContratoVigenteId != nil {
		t.Fatalf("c2: got %+v err %v, want Fletes Baja inactivo sin contrato", cl, err)
	}
	_, err = m.Consultas().ObtenerCliente(ctx, uuid.MustParse("00000000-0000-0000-0000-0000000000c9"))
	if got := platformerrors.AsCategory(err); got != platformerrors.CategoryNoEncontrado {
		t.Fatalf("inexistente: got categoría %v (err %v), want NoEncontrado", got, err)
	}

	mux := http.NewServeMux()
	m.Routes(mux)
	for _, tc := range []struct {
		ruta   string
		status int
		cuerpo string
	}{
		{"/api/v1/clientes/00000000-0000-0000-0000-0000000000c1", http.StatusOK, `"contrato_vigente_id":"00000000-0000-0000-0000-0000000000a1"`},
		{"/api/v1/clientes/00000000-0000-0000-0000-0000000000c2", http.StatusOK, `"estado":"inactivo"`},
		{"/api/v1/clientes/00000000-0000-0000-0000-0000000000c9", http.StatusNotFound, ""},
		{"/api/v1/clientes/no-es-id", http.StatusBadRequest, ""},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.ruta, nil))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.cuerpo) {
			t.Errorf("GET %s: status %d cuerpo %s; quiere %d con %q", tc.ruta, rec.Code, rec.Body, tc.status, tc.cuerpo)
		}
	}
}

// clienteDeVehiculo lee el cliente_id de la versión vigente del vehículo; nil si
// es NULL. Va por admin: el contrato no expone cliente_id en Vehiculo.
func clienteDeVehiculo(t *testing.T, ctx context.Context, admin *pgx.Conn, id string) *uuid.UUID {
	t.Helper()
	var c *uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT cliente_id FROM catalogos.vehiculo WHERE id = $1 AND vigente_hasta IS NULL`, id).Scan(&c); err != nil {
		t.Fatalf("leer cliente_id de %s: %v", id, err)
	}
	return c
}

// TestCrearVehiculo_FlotaSinCliente (DEC-143 #2): el alta por POST guarda el
// vehículo sin cliente, traiga o no `cliente_id` el cuerpo.
func TestCrearVehiculo_FlotaSinCliente(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	mux := http.NewServeMux()
	m.Routes(mux)

	for _, cuerpo := range []string{
		`{"tipo":"coche","estado":"activa","economico":"P-101","cliente_id":"00000000-0000-0000-0000-0000000000c1"}`,
		`{"tipo":"moto","estado":"activa","economico":"M-7"}`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/vehiculos", strings.NewReader(cuerpo)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST %s: status %d cuerpo %s; quiere 201", cuerpo, rec.Code, rec.Body)
		}
		var v catclient.Vehiculo
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decodificar respuesta: %v", err)
		}
		if c := clienteDeVehiculo(t, ctx, admin, v.Id.String()); c != nil {
			t.Errorf("POST %s: cliente_id = %s; quiere NULL (la flota es de AI27)", cuerpo, c)
		}
	}
}

// TestActualizarVehiculo_ConservaCliente (DEC-143 #2): la nueva versión hereda
// el cliente_id de la anterior tal cual, sin inventarlo: NULL sigue NULL y el
// dato heredado de antes de DEC-143 se conserva.
func TestActualizarVehiculo_ConservaCliente(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	const heredado = "00000000-0000-0000-0000-0000000000c1"
	dbtest.Exec(t, ctx, admin, `INSERT INTO catalogos.vehiculo (id, version, tipo, estado, cliente_id) VALUES
		('00000000-0000-0000-0000-0000000000b1', 1, 'coche', 'activa', '`+heredado+`'),
		('00000000-0000-0000-0000-0000000000b2', 1, 'coche', 'activa', NULL)`)

	placas := "XYZ-1"
	for id, quiere := range map[string]string{
		"00000000-0000-0000-0000-0000000000b1": heredado,
		"00000000-0000-0000-0000-0000000000b2": "",
	} {
		v, err := m.Comandos().ActualizarVehiculo(ctx, uuid.MustParse(id), catclient.VehiculoCambios{Placas: &placas})
		if err != nil {
			t.Fatalf("ActualizarVehiculo(%s): %v", id, err)
		}
		if v.Version == nil || *v.Version != 2 {
			t.Fatalf("%s: version %v; quiere 2", id, v.Version)
		}
		got := ""
		if c := clienteDeVehiculo(t, ctx, admin, id); c != nil {
			got = c.String()
		}
		if got != quiere {
			t.Errorf("%s: cliente_id = %q; quiere %q", id, got, quiere)
		}
	}
}

// TestAptitudVehiculo_SinPertenencia (DEC-143 #3): la aptitud se evalúa por el
// vehículo mismo. Uno activo es apto para viajes de dos clientes distintos, uno
// inactivo no lo es para ninguno, y uno inexistente sigue siendo NoEncontrado.
func TestAptitudVehiculo_SinPertenencia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := moduloConCatalogos(t, ctx)
	clienteA := uuid.MustParse("00000000-0000-0000-0000-0000000000c1")
	clienteB := uuid.MustParse("00000000-0000-0000-0000-0000000000c2")

	crear := func(estado catclient.VehiculoEntradaEstado) catclient.Id {
		v, err := m.Comandos().CrearVehiculo(ctx, catclient.VehiculoEntrada{Tipo: catclient.Coche, Estado: estado})
		if err != nil {
			t.Fatalf("CrearVehiculo(%s): %v", estado, err)
		}
		return v.Id
	}
	activo, inactivo := crear(catclient.Activa), crear(catclient.Inactiva)

	for _, cliente := range []catclient.Id{clienteA, clienteB} {
		ap, err := m.Consultas().AptitudVehiculo(ctx, activo, cliente)
		if err != nil || !ap.Apto {
			t.Errorf("activo, cliente %s: aptitud %+v err %v; quiere apto", cliente, ap, err)
		}
		ap, err = m.Consultas().AptitudVehiculo(ctx, inactivo, cliente)
		if err != nil || ap.Apto || ap.Motivos == nil || len(*ap.Motivos) != 1 || (*ap.Motivos)[0] != "vehículo inactivo" {
			t.Errorf("inactivo, cliente %s: aptitud %+v err %v; quiere no apto por \"vehículo inactivo\"", cliente, ap, err)
		}
	}

	_, err := m.Consultas().AptitudVehiculo(ctx, uuid.MustParse("00000000-0000-0000-0000-0000000000b9"), clienteA)
	if got := platformerrors.AsCategory(err); got != platformerrors.CategoryNoEncontrado {
		t.Fatalf("inexistente: categoría %v (err %v); quiere NoEncontrado", got, err)
	}
}

// publicadorRegistro es un bus.Publisher que solo guarda lo que recibe.
type publicadorRegistro struct {
	mu     sync.Mutex
	sobres []bus.Sobre
}

func (p *publicadorRegistro) Publish(_ context.Context, s bus.Sobre) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sobres = append(p.sobres, s)
	return nil
}

func (p *publicadorRegistro) copia() []bus.Sobre {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bus.Sobre(nil), p.sobres...)
}

// TestRelayPublicaCatalogoActualizado (DT-22): con BusPublisher, el módulo
// aporta el relay de su outbox; el alta de un vehículo sale al bus como
// catalogo.actualizado y su fila queda publicada. Sin BusPublisher no hay
// workers (standalone sin bus).
func TestRelayPublicaCatalogoActualizado(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if w := moduloConCatalogos(t, ctx).Workers(); len(w) != 0 {
		t.Fatalf("sin BusPublisher: %d workers; quiere 0", len(w))
	}

	pub := &publicadorRegistro{}
	m := moduloConDeps(t, ctx, catalogos.Deps{BusPublisher: pub})
	workers := m.Workers()
	if len(workers) != 1 || workers[0].Name() != "outbox-relay:catalogos" {
		t.Fatalf("workers = %v; quiere solo outbox-relay:catalogos", workers)
	}
	v, err := m.Comandos().CrearVehiculo(ctx, catclient.VehiculoEntrada{Tipo: catclient.Coche, Estado: catclient.Activa})
	if err != nil {
		t.Fatalf("CrearVehiculo: %v", err)
	}

	runCtx, stop := context.WithCancel(ctx)
	hecho := make(chan error, 1)
	go func() { hecho <- workers[0].Run(runCtx) }()
	for len(pub.copia()) == 0 && ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	if err := <-hecho; err != nil {
		t.Fatalf("relay.Run: %v", err)
	}

	sobres := pub.copia()
	if len(sobres) != 1 || sobres[0].Tipo != "catalogo.actualizado" || sobres[0].AgregadoID != v.Id.String() {
		t.Fatalf("sobres = %+v; quiere un catalogo.actualizado de %s", sobres, v.Id)
	}
	admin, closeAdmin := dbtest.AdminConn(t, ctx)
	defer closeAdmin()
	var pendientes int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM catalogos.outbox WHERE publicado = false`).Scan(&pendientes); err != nil {
		t.Fatalf("contar pendientes: %v", err)
	}
	if pendientes != 0 {
		t.Errorf("filas pendientes en catalogos.outbox = %d; quiere 0", pendientes)
	}
}
