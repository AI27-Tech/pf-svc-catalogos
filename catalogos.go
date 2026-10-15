// Package catalogos es el registro maestro de las entidades sobre las que la
// operación trabaja: clientes, contratos, SLA, vehículos de AI27 (coche/moto),
// custodios y catálogos de tipo. Es una HOJA del grafo (catalogo -> []): no
// llama a operacion ni a ningún módulo de dominio. Su única arista saliente es
// la restringida a identidad para revocación de sesión (DEC-051/058).
//
// Frontera (ADR 31): catalogos es dueño de los ÁTOMOS (vehículo, custodio);
// NO de la `unidad` (paquete vehículo + custodios), su roster, disponibilidad
// ni reserva, que son de operacion. catalogos responde "qué es y si es apto";
// operacion responde "si está libre y la toma". No existe aquí una bandera
// `disponible`, y en V1 la APTITUD ES INFORMATIVA: no gatea nada (DEC-055).
//
// Este archivo es la superficie pública del módulo (00 "Forma de un módulo").
// Scaffolding inicial: el cableado existe, la lógica de negocio real no.
package catalogos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	platformerrors "github.com/AI27-Tech/pf-pkg-platform-go/errors"
	"github.com/AI27-Tech/pf-pkg-platform-go/pgxpool"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/custodios"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/tipos"
	"github.com/AI27-Tech/pf-svc-catalogos/internal/vehiculos"
	"github.com/google/uuid"
)

// Pools agrupa los pools por rol que el módulo necesita. catalogos usa un solo
// esquema y un solo rol (`svc_catalogos`), así que recibe un único pool
// (00 regla 4). No comparte pool entre roles.
type Pools struct {
	Catalogos *pgxpool.Pool
}

// Deps agrupa los puertos de salida que la raíz resuelve por el consumidor.
// catalogos es hoja: su único puerto es la revocación de sesión (restringida a
// identidad, DEC-051). Se deja como dependencia opcional para no bloquear el
// esqueleto; su cableado real llega con el consumidor de sesion.revocada.
type Deps struct {
	// Revocacion consulta la lista de sesiones revocadas al arrancar y una
	// sesión puntual en modo degradado (ADR 26 §4–§5). Puede ser nil en el
	// esqueleto; entonces la autorización corre sin proyección de tombstones.
	Revocacion RevocacionPort
}

// RevocacionPort es la arista restringida a identidad (solo dos operaciones,
// DEC-051). La declara el consumidor (este módulo); la raíz la implementa con
// el adaptador en proceso (dentro de core) o HTTP. Scaffolding: la interfaz
// existe, no hay implementación cableada aquí.
type RevocacionPort interface {
	// ListarRevocadas devuelve las sesiones revocadas para el snapshot inicial.
	ListarRevocadas(ctx context.Context) ([]string, error)
	// ObtenerSesion consulta una sesión puntual en modo degradado.
	ObtenerSesion(ctx context.Context, sesionID string) (revocada bool, err error)
}

// PresupuestoRevocacion es el presupuesto de tiempo de la arista restringida a
// identidad (RevocacionPort). Se exporta JUNTO AL PUERTO por la obligación I3
// (ADR 17): el adaptador HTTP de este repo y el EN PROCESO de pf-svc-core deben
// usar EXACTAMENTE el mismo valor; core lo lee de aquí y no duplica el número.
//
// Es el presupuesto de UNA llamada puntual (snapshot al arrancar o fallback
// ObtenerSesion), no el umbral de frescura del canal: ese umbral (15 s sin
// frescura antes del fallback, 30 s de corte máximo) lo fija ADR 26 y es
// independiente del timeout de una sola llamada HTTP. El valor concreto no está
// en las fuentes; 2 s es el default común, y no es un número de negocio aprobado.
const PresupuestoRevocacion = 2 * time.Second

// Module es la instancia del módulo catalogos ya construida.
type Module struct {
	pools Pools
	deps  Deps
	// Casos de uso por sub-módulo. Se van agregando conforme se implementan.
	vehiculos *vehiculos.Repositorio
	custodios *custodios.Repositorio
	tipos     *tipos.Repositorio
}

// New construye el módulo con sus pools y puertos ya resueltos. No lee entorno
// (00 regla 4): la raíz le pasa todo construido.
func New(p Pools, d Deps) (*Module, error) {
	return &Module{
		pools:     p,
		deps:      d,
		vehiculos: vehiculos.NewRepositorio(p.Catalogos),
		custodios: custodios.NewRepositorio(p.Catalogos),
		tipos:     tipos.NewRepositorio(p.Catalogos),
	}, nil
}

// Routes monta las rutas HTTP del módulo bajo /api/v1/... en el mux dado. Solo
// monta las operaciones presentes en la versión fijada de catalogos.yaml
// (contrato primero, 00 regla 6). Hoy monta vehículos (E5-T1), custodios
// (E5-T2) y catálogos de tipo (E5-T3), ya cerradas en tracer.8. Las lecturas de
// cliente/contrato/SLA siguen en scaffold (brecha C5), así que no se montan.
func (m *Module) Routes(mux *http.ServeMux) {
	// Vehículos (superficie completa de E5-T1 sobre tracer.6). Los handlers
	// delegan en los MISMOS casos de uso que Consultas()/Comandos() (I3): el
	// adaptador HTTP y el en-proceso no divergen.
	q := m.Consultas()
	cmd := m.Comandos()

	// GET /api/v1/vehiculos — listado paginado, o lote si viene ?ids (C4).
	// `ids` y `cursor` son mutuamente excluyentes (contrato); con ambos, 400.
	mux.HandleFunc("GET /api/v1/vehiculos", func(w http.ResponseWriter, r *http.Request) {
		idsRaw := r.URL.Query().Get("ids")
		cursorRaw := r.URL.Query().Get("cursor")
		if idsRaw != "" && cursorRaw != "" {
			escribirError(w, platformerrors.NewValidacion("ids y cursor son mutuamente excluyentes"))
			return
		}
		if idsRaw != "" {
			ids, err := parsearIDs(idsRaw)
			if err != nil {
				escribirError(w, err)
				return
			}
			pag, err := q.ListarVehiculosPorLote(r.Context(), ids)
			if err != nil {
				escribirError(w, err)
				return
			}
			escribirJSON(w, http.StatusOK, pag)
			return
		}
		var cursor *catclient.Cursor
		if cursorRaw != "" {
			cursor = &cursorRaw
		}
		pag, err := q.ListarVehiculos(r.Context(), cursor)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, pag)
	})

	// POST /api/v1/vehiculos — alta.
	mux.HandleFunc("POST /api/v1/vehiculos", func(w http.ResponseWriter, r *http.Request) {
		var entrada catclient.VehiculoEntrada
		if err := json.NewDecoder(r.Body).Decode(&entrada); err != nil {
			escribirError(w, platformerrors.NewValidacion("cuerpo inválido"))
			return
		}
		v, err := cmd.CrearVehiculo(r.Context(), entrada)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusCreated, v)
	})

	// GET /api/v1/vehiculos/{id} — vigente, o ?a_fecha (C9).
	mux.HandleFunc("GET /api/v1/vehiculos/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("id de vehículo inválido"))
			return
		}
		aFecha, err := parsearAFecha(r.URL.Query().Get("a_fecha"))
		if err != nil {
			escribirError(w, err)
			return
		}
		v, err := q.ObtenerVehiculo(r.Context(), id, aFecha)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, v)
	})

	// PATCH /api/v1/vehiculos/{id} — nueva versión.
	mux.HandleFunc("PATCH /api/v1/vehiculos/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("id de vehículo inválido"))
			return
		}
		var cambios catclient.VehiculoCambios
		if err := json.NewDecoder(r.Body).Decode(&cambios); err != nil {
			escribirError(w, platformerrors.NewValidacion("cuerpo inválido"))
			return
		}
		v, err := cmd.ActualizarVehiculo(r.Context(), id, cambios)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, v)
	})

	// GET /api/v1/vehiculos/{id}/aptitud — aptitud informativa.
	mux.HandleFunc("GET /api/v1/vehiculos/{id}/aptitud", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("id de vehículo inválido"))
			return
		}
		clienteRaw := r.URL.Query().Get("cliente_id")
		if clienteRaw == "" {
			escribirError(w, platformerrors.NewValidacion("cliente_id es obligatorio"))
			return
		}
		clienteID, err := uuid.Parse(clienteRaw)
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("cliente_id inválido"))
			return
		}
		ap, err := q.AptitudVehiculo(r.Context(), id, clienteID)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, ap)
	})

	// --- Custodios (superficie completa de E5-T2). Mismo patrón que vehículos:
	// los handlers delegan en los MISMOS casos de uso que Consultas()/Comandos()
	// (I3).

	// GET /api/v1/custodios — listado paginado, o lote si viene ?ids (C4).
	// `ids` y `cursor` son mutuamente excluyentes (contrato); con ambos, 400.
	mux.HandleFunc("GET /api/v1/custodios", func(w http.ResponseWriter, r *http.Request) {
		idsRaw := r.URL.Query().Get("ids")
		cursorRaw := r.URL.Query().Get("cursor")
		if idsRaw != "" && cursorRaw != "" {
			escribirError(w, platformerrors.NewValidacion("ids y cursor son mutuamente excluyentes"))
			return
		}
		if idsRaw != "" {
			ids, err := parsearIDs(idsRaw)
			if err != nil {
				escribirError(w, err)
				return
			}
			pag, err := q.ListarCustodiosPorLote(r.Context(), ids)
			if err != nil {
				escribirError(w, err)
				return
			}
			escribirJSON(w, http.StatusOK, pag)
			return
		}
		var cursor *catclient.Cursor
		if cursorRaw != "" {
			cursor = &cursorRaw
		}
		pag, err := q.ListarCustodios(r.Context(), cursor)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, pag)
	})

	// POST /api/v1/custodios — alta.
	mux.HandleFunc("POST /api/v1/custodios", func(w http.ResponseWriter, r *http.Request) {
		var entrada catclient.CustodioEntrada
		if err := json.NewDecoder(r.Body).Decode(&entrada); err != nil {
			escribirError(w, platformerrors.NewValidacion("cuerpo inválido"))
			return
		}
		cst, err := cmd.CrearCustodio(r.Context(), entrada)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusCreated, cst)
	})

	// GET /api/v1/custodios/{id} — vigente, o ?a_fecha (C9).
	mux.HandleFunc("GET /api/v1/custodios/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("id de custodio inválido"))
			return
		}
		aFecha, err := parsearAFecha(r.URL.Query().Get("a_fecha"))
		if err != nil {
			escribirError(w, err)
			return
		}
		cst, err := q.ObtenerCustodio(r.Context(), id, aFecha)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, cst)
	})

	// PATCH /api/v1/custodios/{id} — nueva versión.
	mux.HandleFunc("PATCH /api/v1/custodios/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("id de custodio inválido"))
			return
		}
		var cambios catclient.CustodioCambios
		if err := json.NewDecoder(r.Body).Decode(&cambios); err != nil {
			escribirError(w, platformerrors.NewValidacion("cuerpo inválido"))
			return
		}
		cst, err := cmd.ActualizarCustodio(r.Context(), id, cambios)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, cst)
	})

	// GET /api/v1/custodios/{id}/aptitud — aptitud informativa (DEC-055).
	mux.HandleFunc("GET /api/v1/custodios/{id}/aptitud", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("id de custodio inválido"))
			return
		}
		clienteRaw := r.URL.Query().Get("cliente_id")
		if clienteRaw == "" {
			escribirError(w, platformerrors.NewValidacion("cliente_id es obligatorio"))
			return
		}
		clienteID, err := uuid.Parse(clienteRaw)
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("cliente_id inválido"))
			return
		}
		ap, err := q.AptitudCustodio(r.Context(), id, clienteID)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, ap)
	})

	// --- Catálogos de tipo (E5-T3). UN solo recurso parametrizado por `{tipo}`
	// para las cinco listas de valores de V1 (contrato tracer.8, opción A). El
	// `{tipo}` se valida contra el enum TipoCatalogo; uno inválido es 400 (no
	// 404), porque es un valor de ruta mal formado, no un recurso ausente.

	// GET /api/v1/catalogos-tipo/{tipo} — lectura por lote (paginada por cursor).
	mux.HandleFunc("GET /api/v1/catalogos-tipo/{tipo}", func(w http.ResponseWriter, r *http.Request) {
		tipo := catclient.TipoCatalogo(r.PathValue("tipo"))
		var cursor *catclient.Cursor
		if cursorRaw := r.URL.Query().Get("cursor"); cursorRaw != "" {
			cursor = &cursorRaw
		}
		pag, err := q.ListarValoresCatalogo(r.Context(), tipo, cursor)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, pag)
	})

	// POST /api/v1/catalogos-tipo/{tipo} — alta de un valor (versión 1).
	mux.HandleFunc("POST /api/v1/catalogos-tipo/{tipo}", func(w http.ResponseWriter, r *http.Request) {
		tipo := catclient.TipoCatalogo(r.PathValue("tipo"))
		var entrada catclient.ValorCatalogoEntrada
		if err := json.NewDecoder(r.Body).Decode(&entrada); err != nil {
			escribirError(w, platformerrors.NewValidacion("cuerpo inválido"))
			return
		}
		v, err := cmd.CrearValorCatalogo(r.Context(), tipo, entrada)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusCreated, v)
	})

	// PATCH /api/v1/catalogos-tipo/{tipo}/{id} — nueva versión (modificar o
	// desactivar). Desactivar es enviar estado: inactivo por esta misma ruta.
	mux.HandleFunc("PATCH /api/v1/catalogos-tipo/{tipo}/{id}", func(w http.ResponseWriter, r *http.Request) {
		tipo := catclient.TipoCatalogo(r.PathValue("tipo"))
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			escribirError(w, platformerrors.NewValidacion("id de valor de catálogo inválido"))
			return
		}
		var cambios catclient.ValorCatalogoCambios
		if err := json.NewDecoder(r.Body).Decode(&cambios); err != nil {
			escribirError(w, platformerrors.NewValidacion("cuerpo inválido"))
			return
		}
		v, err := cmd.ActualizarValorCatalogo(r.Context(), tipo, id, cambios)
		if err != nil {
			escribirError(w, err)
			return
		}
		escribirJSON(w, http.StatusOK, v)
	})
}

// parsearIDs convierte la lista separada por coma del query param `ids` a UUIDs.
func parsearIDs(raw string) ([]catclient.Id, error) {
	partes := strings.Split(raw, ",")
	ids := make([]catclient.Id, 0, len(partes))
	for _, p := range partes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, platformerrors.NewValidacion("id inválido en lote: %s", p)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, platformerrors.NewValidacion("ids no puede estar vacío")
	}
	return ids, nil
}

// parsearAFecha interpreta el query param `a_fecha` (RFC 3339). Vacío ⇒ nil.
func parsearAFecha(raw string) (*catclient.MarcaTiempo, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, platformerrors.NewValidacion("a_fecha inválida (se espera RFC 3339)")
	}
	return &t, nil
}

// escribirJSON serializa v como JSON con el status dado.
func escribirJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// escribirError traduce un error de la taxonomía de la plataforma a su status
// HTTP (invariante I3: la misma categoría se traduce igual en HTTP y en proceso).
// El `codigo` del contrato es un valor de máquina estable; se deriva de la
// categoría de la plataforma.
func escribirError(w http.ResponseWriter, err error) {
	cat := platformerrors.AsCategory(err)
	escribirJSON(w, cat.HTTPStatus(), catclient.Error{
		Codigo:  codigoDe(cat),
		Mensaje: err.Error(),
	})
}

// codigoDe mapea la categoría de la plataforma a un código de máquina estable en
// snake_case (los consumidores ramifican sobre `codigo`, no sobre `mensaje`).
func codigoDe(c platformerrors.Category) string {
	switch c {
	case platformerrors.CategoryNoAutorizado:
		return "no_autorizado"
	case platformerrors.CategoryNoEncontrado:
		return "no_encontrado"
	case platformerrors.CategoryValidacion:
		return "validacion"
	case platformerrors.CategoryConflicto:
		return "conflicto"
	case platformerrors.CategoryDegradado:
		return "degradado"
	case platformerrors.CategoryRateLimit:
		return "rate_limit"
	default:
		return "roto"
	}
}

// Workers devuelve los workers embebidos del módulo (ADR 29 §6). catalogos
// tiene al menos dos: el relay de su outbox y el consumidor de sesion.revocada
// (DEC-057/058). La raíz decide arrancarlos.
//
// Scaffolding: hoy devuelve una lista vacía. El relay se cablea cuando exista el
// adaptador de bus que la raíz inyecte (bus/memory en desarrollo; broker real en
// espera de ADR 32); el consumidor de sesion.revocada, cuando el evento esté en
// la versión fijada del contrato y la arista a identidad esté resuelta.
func (m *Module) Workers() []worker.Worker {
	return nil
}

// Consultas expone los casos de uso que otros módulos consumen en proceso
// (operacion, reportes, bff; mensajeria siempre por HTTP). Tipada SOLO con tipos
// de pf-pkg-contracts, nunca con tipos de internal/ (00 regla).
func (m *Module) Consultas() ConsultasAPI {
	return &consultas{m: m}
}

// ConsultasAPI es la superficie síncrona de catalogos. Cada método corresponde
// 1:1 a un operationId presente en catalogos.yaml de la versión fijada; no hay
// métodos sin operación contractual (00 regla 6).
//
// Expone lo que el contrato tracer.8 ya tiene para catalogos: la superficie de
// vehículos (E5-T1) y custodios (E5-T2) —listado paginado y por lote, lectura
// vigente o a una fecha, y aptitud—, y la lectura por lote de los catálogos de
// tipo (E5-T3). La lectura de cliente/contrato/SLA sigue declarada pero en
// scaffold. El átomo es `Vehiculo` (ADR 31 §5), no `Unidad`, y la aptitud se
// evalúa contra el contrato/SLA de un cliente (de ahí `clienteID`). La aptitud
// es INFORMATIVA (DEC-055): nunca gatea.
type ConsultasAPI interface {
	ObtenerCliente(ctx context.Context, id catclient.Id) (*catclient.Cliente, error)
	ObtenerContrato(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Contrato, error)
	ObtenerSla(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Sla, error)
	// ListarVehiculos devuelve una página de vehículos vigentes (paginación por
	// cursor; cursor nil arranca desde el principio).
	ListarVehiculos(ctx context.Context, cursor *catclient.Cursor) (*catclient.PaginaVehiculos, error)
	// ListarVehiculosPorLote devuelve los vehículos vigentes de ids, en un solo
	// viaje (C4): evita el patrón N+1 de los consumidores.
	ListarVehiculosPorLote(ctx context.Context, ids []catclient.Id) (*catclient.PaginaVehiculos, error)
	// ObtenerVehiculo devuelve el vehículo vigente, o el vigente a una fecha
	// cuando aFecha no es nil (C9, lectura probatoria P8).
	ObtenerVehiculo(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Vehiculo, error)
	// AptitudVehiculo y AptitudCustodio devuelven aptitud INFORMATIVA (DEC-055):
	// nunca gatean, y "desconocida" es un valor válido si el dato falta. En
	// vehiculo, clienteID acota la evaluación al contrato/SLA de ese cliente; en
	// custodio se acepta por conformidad de contrato pero no acota (el custodio
	// no pertenece a un cliente — ver custodios.Aptitud).
	AptitudVehiculo(ctx context.Context, vehiculoID, clienteID catclient.Id) (*catclient.Aptitud, error)
	AptitudCustodio(ctx context.Context, custodioID, clienteID catclient.Id) (*catclient.Aptitud, error)
	// ListarCustodios devuelve una página de custodios vigentes (paginación por
	// cursor; cursor nil arranca desde el principio).
	ListarCustodios(ctx context.Context, cursor *catclient.Cursor) (*catclient.PaginaCustodios, error)
	// ListarCustodiosPorLote devuelve los custodios vigentes de ids, en un solo
	// viaje (C4): evita el patrón N+1 de los consumidores.
	ListarCustodiosPorLote(ctx context.Context, ids []catclient.Id) (*catclient.PaginaCustodios, error)
	// ObtenerCustodio devuelve el custodio vigente, o el vigente a una fecha
	// cuando aFecha no es nil (C9, lectura probatoria P8).
	ObtenerCustodio(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Custodio, error)
	// ListarValoresCatalogo devuelve una página de valores vigentes de uno de los
	// cinco catálogos de tipo de V1 (E5-T3), paginada por cursor; cursor nil
	// arranca desde el principio. Un `tipo` fuera del enum es error de validación.
	ListarValoresCatalogo(ctx context.Context, tipo catclient.TipoCatalogo, cursor *catclient.Cursor) (*catclient.PaginaValoresCatalogo, error)
}

// ComandosAPI es la superficie de ESCRITURA administrativa de catalogos (E5,
// brecha C7). Separada de ConsultasAPI (solo lectura) porque mutar un vehículo
// crea una nueva versión y emite `catalogo.actualizado` en la misma transacción
// (P17), una responsabilidad distinta de leer.
type ComandosAPI interface {
	// CrearVehiculo da de alta un vehículo (versión 1). clienteID se toma del
	// cuerpo del contrato (VehiculoEntrada.ClienteId).
	CrearVehiculo(ctx context.Context, e catclient.VehiculoEntrada) (*catclient.Vehiculo, error)
	// ActualizarVehiculo crea una nueva versión con los campos presentes, sin
	// sobrescribir la anterior (P8).
	ActualizarVehiculo(ctx context.Context, id catclient.Id, c catclient.VehiculoCambios) (*catclient.Vehiculo, error)
	// CrearCustodio da de alta un custodio (versión 1). A diferencia de vehículo,
	// no toma cliente_id: el custodio no pertenece a un cliente.
	CrearCustodio(ctx context.Context, e catclient.CustodioEntrada) (*catclient.Custodio, error)
	// ActualizarCustodio crea una nueva versión con los campos presentes, sin
	// sobrescribir la anterior (P8).
	ActualizarCustodio(ctx context.Context, id catclient.Id, c catclient.CustodioCambios) (*catclient.Custodio, error)
	// CrearValorCatalogo da de alta un valor en el catálogo `tipo` (versión 1).
	// El `tipo` viaja en la ruta, no en el cuerpo (contrato tracer.8).
	CrearValorCatalogo(ctx context.Context, tipo catclient.TipoCatalogo, e catclient.ValorCatalogoEntrada) (*catclient.ValorCatalogo, error)
	// ActualizarValorCatalogo crea una nueva versión con los campos presentes, sin
	// sobrescribir la anterior (P8). Desactivar un valor es enviar estado
	// `inactivo` por esta vía: no rompe los registros históricos (E5-HU-023).
	ActualizarValorCatalogo(ctx context.Context, tipo catclient.TipoCatalogo, id catclient.Id, c catclient.ValorCatalogoCambios) (*catclient.ValorCatalogo, error)
}

// Comandos expone la superficie de escritura administrativa.
func (m *Module) Comandos() ComandosAPI {
	return &comandos{m: m}
}

// consultas implementa ConsultasAPI delegando en los casos de uso del módulo.
// vehículos, custodios y catálogos de tipo ya están implementados; cliente,
// contrato y SLA siguen devolviendo "no implementado" (scaffold).
type consultas struct {
	m *Module
}

func (c *consultas) ObtenerCliente(ctx context.Context, id catclient.Id) (*catclient.Cliente, error) {
	return nil, errNoImplementado("ObtenerCliente")
}

func (c *consultas) ObtenerContrato(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Contrato, error) {
	return nil, errNoImplementado("ObtenerContrato")
}

func (c *consultas) ObtenerSla(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Sla, error) {
	return nil, errNoImplementado("ObtenerSla")
}

func (c *consultas) ListarVehiculos(ctx context.Context, cursor *catclient.Cursor) (*catclient.PaginaVehiculos, error) {
	pag, err := c.m.vehiculos.Listar(ctx, cursor)
	if err != nil {
		return nil, err
	}
	return &pag, nil
}

func (c *consultas) ListarVehiculosPorLote(ctx context.Context, ids []catclient.Id) (*catclient.PaginaVehiculos, error) {
	pag, err := c.m.vehiculos.ListarPorLote(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &pag, nil
}

func (c *consultas) ObtenerVehiculo(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Vehiculo, error) {
	v, err := c.m.vehiculos.Obtener(ctx, id, aFecha)
	if errors.Is(err, vehiculos.ErrNoEncontrado) {
		return nil, platformerrors.NewNoEncontrado("vehículo no encontrado")
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *consultas) AptitudVehiculo(ctx context.Context, vehiculoID, clienteID catclient.Id) (*catclient.Aptitud, error) {
	ap, err := c.m.vehiculos.Aptitud(ctx, vehiculoID, clienteID, time.Now())
	if errors.Is(err, vehiculos.ErrNoEncontrado) {
		// Traducción a la taxonomía de la plataforma (I3): no encontrado ⇒ 404.
		return nil, platformerrors.NewNoEncontrado("vehículo no encontrado")
	}
	if err != nil {
		return nil, err
	}
	return &ap, nil
}

func (c *consultas) ListarCustodios(ctx context.Context, cursor *catclient.Cursor) (*catclient.PaginaCustodios, error) {
	pag, err := c.m.custodios.Listar(ctx, cursor)
	if err != nil {
		return nil, err
	}
	return &pag, nil
}

func (c *consultas) ListarCustodiosPorLote(ctx context.Context, ids []catclient.Id) (*catclient.PaginaCustodios, error) {
	pag, err := c.m.custodios.ListarPorLote(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &pag, nil
}

func (c *consultas) ObtenerCustodio(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Custodio, error) {
	cst, err := c.m.custodios.Obtener(ctx, id, aFecha)
	if errors.Is(err, custodios.ErrNoEncontrado) {
		return nil, platformerrors.NewNoEncontrado("custodio no encontrado")
	}
	if err != nil {
		return nil, err
	}
	return &cst, nil
}

// AptitudCustodio devuelve aptitud INFORMATIVA del custodio (DEC-055). clienteID
// se recibe por conformidad con el contrato pero NO acota la evaluación.
func (c *consultas) AptitudCustodio(ctx context.Context, custodioID, clienteID catclient.Id) (*catclient.Aptitud, error) {
	_ = clienteID // aceptado pero no usado en V1 (pendiente de decisión de modelado)
	ap, err := c.m.custodios.Aptitud(ctx, custodioID)
	if errors.Is(err, custodios.ErrNoEncontrado) {
		return nil, platformerrors.NewNoEncontrado("custodio no encontrado")
	}
	if err != nil {
		return nil, err
	}
	return &ap, nil
}

func (c *consultas) ListarValoresCatalogo(ctx context.Context, tipo catclient.TipoCatalogo, cursor *catclient.Cursor) (*catclient.PaginaValoresCatalogo, error) {
	pag, err := c.m.tipos.Listar(ctx, tipo, cursor)
	if errors.Is(err, tipos.ErrTipoInvalido) {
		return nil, platformerrors.NewValidacion("tipo de catálogo inválido")
	}
	if err != nil {
		return nil, err
	}
	return &pag, nil
}

// comandos implementa ComandosAPI delegando en el repositorio de cada
// sub-módulo. La escritura de vehículos ya está implementada (C7).
type comandos struct {
	m *Module
}

func (c *comandos) CrearVehiculo(ctx context.Context, e catclient.VehiculoEntrada) (*catclient.Vehiculo, error) {
	v, err := c.m.vehiculos.Crear(ctx, e, e.ClienteId)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *comandos) ActualizarVehiculo(ctx context.Context, id catclient.Id, cambios catclient.VehiculoCambios) (*catclient.Vehiculo, error) {
	v, err := c.m.vehiculos.Actualizar(ctx, id, cambios, time.Now())
	if errors.Is(err, vehiculos.ErrNoEncontrado) {
		return nil, platformerrors.NewNoEncontrado("vehículo no encontrado")
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *comandos) CrearCustodio(ctx context.Context, e catclient.CustodioEntrada) (*catclient.Custodio, error) {
	cst, err := c.m.custodios.Crear(ctx, e)
	if err != nil {
		return nil, err
	}
	return &cst, nil
}

func (c *comandos) ActualizarCustodio(ctx context.Context, id catclient.Id, cambios catclient.CustodioCambios) (*catclient.Custodio, error) {
	cst, err := c.m.custodios.Actualizar(ctx, id, cambios, time.Now())
	if errors.Is(err, custodios.ErrNoEncontrado) {
		return nil, platformerrors.NewNoEncontrado("custodio no encontrado")
	}
	if err != nil {
		return nil, err
	}
	return &cst, nil
}

func (c *comandos) CrearValorCatalogo(ctx context.Context, tipo catclient.TipoCatalogo, e catclient.ValorCatalogoEntrada) (*catclient.ValorCatalogo, error) {
	v, err := c.m.tipos.Crear(ctx, tipo, e)
	if errors.Is(err, tipos.ErrTipoInvalido) {
		return nil, platformerrors.NewValidacion("tipo de catálogo inválido")
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *comandos) ActualizarValorCatalogo(ctx context.Context, tipo catclient.TipoCatalogo, id catclient.Id, cambios catclient.ValorCatalogoCambios) (*catclient.ValorCatalogo, error) {
	v, err := c.m.tipos.Actualizar(ctx, tipo, id, cambios, time.Now())
	if errors.Is(err, tipos.ErrTipoInvalido) {
		return nil, platformerrors.NewValidacion("tipo de catálogo inválido")
	}
	if errors.Is(err, tipos.ErrNoEncontrado) {
		return nil, platformerrors.NewNoEncontrado("valor de catálogo no encontrado")
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
