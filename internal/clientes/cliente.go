package clientes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
	"github.com/AI27-Tech/pf-svc-catalogos/internal"
	"github.com/jackc/pgx/v5"
)

// paginaClientes es el tamaño de página del listado de clientes (selector de
// cliente, E8-T11). Mismo valor que el resto de listados de catalogos.
const paginaClientes = 50

// ConsultasCliente es la superficie mínima de LECTURA que el repositorio necesita:
// solo Query. La cumplen el pool y una pgx.Tx, así que la lectura por id se
// reutiliza dentro de la transacción de escritura.
type ConsultasCliente interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ComandosCliente agrega Begin: el alta y la edición del expediente comprometen
// la versión y `catalogo.actualizado` en la misma transacción (P17). La
// implementa *pgxpool.Pool.
type ComandosCliente interface {
	ConsultasCliente
	Begin(ctx context.Context) (pgx.Tx, error)
}

// RepositorioCliente lee y escribe el expediente de la organización cliente,
// versionado por vigencia (P8): una edición inserta una versión nueva y cierra la
// anterior, nunca la sobrescribe. El alta usa el id del tenant en `identidad`
// (DEC-112, DEC-144 #2).
type RepositorioCliente struct {
	db  ComandosCliente
	pub *outbox.Publisher
}

// NewRepositorioCliente construye el repositorio sobre el pool dado.
func NewRepositorioCliente(db ComandosCliente) *RepositorioCliente {
	return &RepositorioCliente{db: db, pub: outbox.NewPublisher()}
}

// ErrClienteNoEncontrado indica que el cliente no tiene versión vigente.
var ErrClienteNoEncontrado = errors.New("clientes: cliente no encontrado")

// ErrClienteInvalido marca una entrada que no cumple el contrato (nombre, correo
// o teléfono vacíos, correo sin `@`, estado fuera del enum). La fachada lo
// traduce a Validacion (400).
var ErrClienteInvalido = errors.New("clientes: datos inválidos")

// columnasCliente es el SELECT del listado. `contrato_vigente_id` del schema NO
// tiene columna en catalogos.cliente (vive al revés, en catalogos.contrato): el
// listado lo devuelve null, que el contrato permite (es un selector ligero;
// resolverlo exigiría un JOIN que nadie pidió, DEC-101). El orden es el que
// espera escanearClientes.
const columnasCliente = `id, nombre, estado, correo, telefono`

// Listar devuelve una página de clientes ACTIVOS en su versión vigente, paginada
// por cursor (keyset por id ascendente; cursor nil arranca desde el principio).
// Solo clientes `activo`: un cliente inactivo no es asignable a un viaje nuevo
// (DEC-101 #1); el filtro vive aquí porque es el único punto implementable (el
// bff reenvía la página tal cual). El filtro de alcance del jefe_operativo NO lo
// aplica catalogos: no tiene mecanismo de alcance (hoja del grafo, sin rol ni
// vínculo cliente↔usuario); DEC-101 #2 lo atribuye al bff/operacion.
func (r *RepositorioCliente) Listar(ctx context.Context, cursor *catclient.Cursor) (catclient.PaginaClientes, error) {
	desde := ""
	if cursor != nil {
		desde = *cursor
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+columnasCliente+`
		  FROM catalogos.cliente
		 WHERE vigente_hasta IS NULL
		   AND estado = 'activo'
		   AND ($1 = '' OR id > $1::uuid)
		 ORDER BY id
		 LIMIT $2`, desde, paginaClientes+1)
	if err != nil {
		return catclient.PaginaClientes{}, fmt.Errorf("clientes: listar: %w", err)
	}
	defer rows.Close()

	items, err := escanearClientes(rows)
	if err != nil {
		return catclient.PaginaClientes{}, err
	}

	var siguiente *string
	if len(items) > paginaClientes {
		ultimo := items[paginaClientes-1].Id.String()
		siguiente = &ultimo
		items = items[:paginaClientes]
	}
	return catclient.PaginaClientes{Items: items, CursorSiguiente: siguiente}, nil
}

// Obtener devuelve la versión vigente de un cliente, ACTIVO o INACTIVO: a
// diferencia del selector, la lectura por id compone datos de un viaje que ya
// existe (p. ej. el detalle de emergencia de Reacción), y un cliente dado de baja
// después sigue siendo el cliente de ese viaje. Resuelve `contrato_vigente_id`
// (el contrato lo promete: "expediente y su contrato vigente"); el índice
// un_contrato_vigente_por_cliente garantiza a lo más uno. Devuelve
// ErrClienteNoEncontrado si no hay versión vigente.
func (r *RepositorioCliente) Obtener(ctx context.Context, id catclient.Id) (catclient.Cliente, error) {
	return obtener(ctx, r.db, id)
}

// obtener es la lectura por id sobre el pool o sobre la tx de una escritura.
func obtener(ctx context.Context, q ConsultasCliente, id catclient.Id) (catclient.Cliente, error) {
	rows, err := q.Query(ctx, `
		SELECT c.id, c.nombre, c.estado, c.correo, c.telefono,
		       (SELECT ct.id FROM catalogos.contrato ct
		         WHERE ct.cliente_id = c.id AND ct.vigente_hasta IS NULL)
		  FROM catalogos.cliente c
		 WHERE c.id = $1 AND c.vigente_hasta IS NULL`, id)
	if err != nil {
		return catclient.Cliente{}, fmt.Errorf("clientes: obtener: %w", err)
	}
	cl, err := pgx.CollectOneRow(rows, func(row pgx.CollectableRow) (catclient.Cliente, error) {
		var (
			cl     catclient.Cliente
			estado string
		)
		err := row.Scan(&cl.Id, &cl.Nombre, &estado, &cl.Correo, &cl.Telefono, &cl.ContratoVigenteId)
		cl.Estado = catclient.ClienteEstado(estado)
		return cl, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return catclient.Cliente{}, ErrClienteNoEncontrado
	}
	if err != nil {
		return catclient.Cliente{}, fmt.Errorf("clientes: obtener: %w", err)
	}
	return cl, nil
}

// Crear da de alta el cliente con el id dado (el de su tenant, DEC-144 #2), en
// estado `activo`, y emite `catalogo.actualizado` en la misma tx (P17). Es
// idempotente por id: si ya existe un cliente con ese id (en cualquier versión),
// no lo modifica, no emite evento y devuelve el guardado con creado=false.
//
// ON CONFLICT DO NOTHING resuelve también la carrera de dos altas con el mismo
// id: la segunda espera el commit de la primera sobre la PK (id, version) y no
// inserta, así que queda un solo cliente y un solo evento.
func (r *RepositorioCliente) Crear(ctx context.Context, id catclient.Id, e catclient.ClienteEntrada) (cl catclient.Cliente, creado bool, err error) {
	if err := validarEntrada(e); err != nil {
		return catclient.Cliente{}, false, err
	}
	err = pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO catalogos.cliente (id, version, nombre, correo, telefono, estado)
			VALUES ($1, 1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`,
			id, e.Nombre, e.Correo, e.Telefono, string(catclient.ClienteEstadoActivo))
		if err != nil {
			return fmt.Errorf("insertar cliente: %w", err)
		}
		creado = tag.RowsAffected() == 1
		if creado {
			if err := internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
				Entidad: "cliente", EntidadID: id.String(), Version: 1,
			}); err != nil {
				return err
			}
		}
		cl, err = obtener(ctx, tx, id)
		return err
	})
	if err != nil {
		return catclient.Cliente{}, false, fmt.Errorf("clientes: crear: %w", err)
	}
	return cl, creado, nil
}

// Actualizar crea una NUEVA versión con los campos presentes: cierra la vigencia
// de la actual, inserta version+1 y emite `catalogo.actualizado`, todo en una tx
// (P17, P8). Mismo criterio que vehículos y custodios: un PATCH sin cambio
// efectivo también versiona y emite. No bloquea reactivar: la terminalidad de la
// organización la aplica el bff (DEC-139 #4). ErrClienteNoEncontrado si no hay
// versión vigente.
func (r *RepositorioCliente) Actualizar(ctx context.Context, id catclient.Id, c catclient.ClienteCambios, ahora time.Time) (catclient.Cliente, error) {
	if err := validarCambios(c); err != nil {
		return catclient.Cliente{}, err
	}
	var nuevo catclient.Cliente
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var (
			versionActual    int
			nombre, estado   string
			correo, telefono *string
		)
		err := tx.QueryRow(ctx, `
			SELECT version, nombre, estado, correo, telefono
			  FROM catalogos.cliente
			 WHERE id = $1 AND vigente_hasta IS NULL
			 FOR UPDATE`, id).Scan(&versionActual, &nombre, &estado, &correo, &telefono)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrClienteNoEncontrado
		}
		if err != nil {
			return fmt.Errorf("leer versión vigente: %w", err)
		}

		if c.Nombre != nil {
			nombre = *c.Nombre
		}
		if c.Estado != nil {
			estado = string(*c.Estado)
		}
		if c.Correo != nil {
			correo = c.Correo
		}
		if c.Telefono != nil {
			telefono = c.Telefono
		}
		nuevaVersion := versionActual + 1

		// Cerrar la vigencia de la versión actual (único UPDATE permitido).
		if _, err := tx.Exec(ctx, `
			UPDATE catalogos.cliente SET vigente_hasta = $1
			 WHERE id = $2 AND version = $3`, ahora, id, versionActual); err != nil {
			return fmt.Errorf("cerrar vigencia: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO catalogos.cliente
			  (id, version, nombre, correo, telefono, estado, vigente_desde)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id, nuevaVersion, nombre, correo, telefono, estado, ahora); err != nil {
			return fmt.Errorf("insertar nueva versión: %w", err)
		}
		if err := internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "cliente", EntidadID: id.String(), Version: nuevaVersion,
		}); err != nil {
			return err
		}
		nuevo, err = obtener(ctx, tx, id)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrClienteNoEncontrado) {
			return catclient.Cliente{}, err
		}
		return catclient.Cliente{}, fmt.Errorf("clientes: actualizar: %w", err)
	}
	return nuevo, nil
}

// validarEntrada aplica ClienteEntrada: los tres campos obligatorios y no vacíos
// (minLength 1) y el correo con `@` (DEC-139 #2, sin más reglas de formato).
func validarEntrada(e catclient.ClienteEntrada) error {
	return validarCampos(&e.Nombre, &e.Correo, &e.Telefono)
}

// validarCambios aplica ClienteCambios: los campos presentes con las mismas
// reglas que el alta, y `estado` dentro del enum.
func validarCambios(c catclient.ClienteCambios) error {
	if err := validarCampos(c.Nombre, c.Correo, c.Telefono); err != nil {
		return err
	}
	if c.Estado != nil && *c.Estado != catclient.ClienteCambiosEstadoActivo && *c.Estado != catclient.ClienteCambiosEstadoInactivo {
		return fmt.Errorf("%w: estado debe ser activo o inactivo", ErrClienteInvalido)
	}
	return nil
}

// validarCampos revisa cada campo presente (no nil). Vacío es la cadena "", tal
// como lo fija minLength 1 del contrato.
func validarCampos(nombre, correo, telefono *string) error {
	if nombre != nil && *nombre == "" {
		return fmt.Errorf("%w: nombre es obligatorio", ErrClienteInvalido)
	}
	if correo != nil && !strings.Contains(*correo, "@") {
		return fmt.Errorf("%w: correo debe contener @", ErrClienteInvalido)
	}
	if telefono != nil && *telefono == "" {
		return fmt.Errorf("%w: teléfono es obligatorio", ErrClienteInvalido)
	}
	return nil
}

// escanearClientes mapea las filas (con columnas = `columnasCliente`) a
// []Cliente. `contrato_vigente_id` queda nil (sin columna en la tabla cliente).
func escanearClientes(rows pgx.Rows) ([]catclient.Cliente, error) {
	items := make([]catclient.Cliente, 0)
	for rows.Next() {
		var cl catclient.Cliente
		var estado string
		if err := rows.Scan(&cl.Id, &cl.Nombre, &estado, &cl.Correo, &cl.Telefono); err != nil {
			return nil, fmt.Errorf("clientes: escanear cliente: %w", err)
		}
		cl.Estado = catclient.ClienteEstado(estado)
		items = append(items, cl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clientes: filas de cliente: %w", err)
	}
	return items, nil
}
