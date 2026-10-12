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
	"net/http"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-pkg-platform-go/pgxpool"
	"github.com/AI27-Tech/pf-pkg-platform-go/worker"
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
	// Aquí vivirán los casos de uso de cada sub-módulo cuando se implementen.
}

// New construye el módulo con sus pools y puertos ya resueltos. No lee entorno
// (00 regla 4): la raíz le pasa todo construido.
func New(p Pools, d Deps) (*Module, error) {
	return &Module{pools: p, deps: d}, nil
}

// Routes monta las rutas HTTP del módulo bajo /api/v1/... en el mux dado.
// Scaffolding: hoy no monta ninguna ruta de negocio. Las rutas se agregan una a
// una conforme se implementen los casos de uso, y SOLO las presentes en la
// versión fijada de catalogos.yaml (contrato primero). Las rutas de vehículo,
// lote, administración y catálogos de tipo están bloqueadas por brechas de
// contrato (ver README, brechas C3–C10).
func (m *Module) Routes(mux *http.ServeMux) {
	// TODO(catalogos): montar handlers conforme se implementen los casos de uso,
	// delegando en los mismos casos de uso que Consultas() (I3).
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
// Hoy expone solo lo que el contrato ya tiene: lectura de clientes, contrato y
// SLA, y lectura + aptitud de custodios y vehículos. Las lecturas por lote
// (C4/C5) y a una fecha de átomos (C9) NO están en el contrato: no se agregan
// aquí. C3 resuelta en tracer.4: el átomo es `Vehiculo` (ADR 31 §5), no `Unidad`,
// y la aptitud se evalúa contra el contrato/SLA de un cliente (de ahí `clienteID`).
type ConsultasAPI interface {
	ObtenerCliente(ctx context.Context, id catclient.Id) (*catclient.Cliente, error)
	ObtenerContrato(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Contrato, error)
	ObtenerSla(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (*catclient.Sla, error)
	// AptitudVehiculo y AptitudCustodio devuelven aptitud INFORMATIVA (DEC-055):
	// nunca gatean, y "desconocida" es un valor válido si el dato falta. clienteID
	// acota la evaluación al contrato/SLA de ese cliente (tracer.4).
	AptitudVehiculo(ctx context.Context, vehiculoID, clienteID catclient.Id) (*catclient.Aptitud, error)
	AptitudCustodio(ctx context.Context, custodioID, clienteID catclient.Id) (*catclient.Aptitud, error)
}

// consultas implementa ConsultasAPI delegando en los casos de uso del módulo.
// Scaffolding: todos los métodos devuelven "no implementado" todavía; la
// implementación real llega con cada sub-módulo.
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

func (c *consultas) AptitudVehiculo(ctx context.Context, vehiculoID, clienteID catclient.Id) (*catclient.Aptitud, error) {
	return nil, errNoImplementado("AptitudVehiculo")
}

func (c *consultas) AptitudCustodio(ctx context.Context, custodioID, clienteID catclient.Id) (*catclient.Aptitud, error) {
	return nil, errNoImplementado("AptitudCustodio")
}
