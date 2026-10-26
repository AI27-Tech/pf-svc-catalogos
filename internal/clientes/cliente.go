package clientes

import (
	"context"
	"fmt"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/jackc/pgx/v5"
)

// paginaClientes es el tamaño de página del listado de clientes (selector de
// cliente, E8-T11). Mismo valor que el resto de listados de catalogos.
const paginaClientes = 50

// ConsultasCliente es la superficie mínima de LECTURA que el listado de clientes
// necesita del pool: solo Query. Se declara como interfaz para poder sustituirla
// en pruebas sin una base real.
type ConsultasCliente interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// RepositorioCliente lee la versión vigente de los clientes. Solo lectura: el
// alta/baja del expediente es superficie administrativa pendiente (brecha C7);
// este repositorio existe para el selector de cliente al crear un viaje por
// Operación (E8-T11, DEC-026 #3).
type RepositorioCliente struct {
	db ConsultasCliente
}

// NewRepositorioCliente construye el repositorio sobre el pool dado.
func NewRepositorioCliente(db ConsultasCliente) *RepositorioCliente {
	return &RepositorioCliente{db: db}
}

// columnasCliente es el SELECT del listado. `contrato_vigente_id` del schema NO
// tiene columna en catalogos.cliente (vive al revés, en catalogos.contrato): el
// listado lo devuelve null, que el contrato permite (es un selector ligero;
// resolverlo exigiría un JOIN que nadie pidió, DEC-101). El orden es el que
// espera escanearClientes.
const columnasCliente = `id, nombre, estado`

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

// escanearClientes mapea las filas (con columnas = `columnasCliente`) a
// []Cliente. `contrato_vigente_id` queda nil (sin columna en la tabla cliente).
func escanearClientes(rows pgx.Rows) ([]catclient.Cliente, error) {
	items := make([]catclient.Cliente, 0)
	for rows.Next() {
		var (
			id     catclient.Id
			nombre string
			estado string
		)
		if err := rows.Scan(&id, &nombre, &estado); err != nil {
			return nil, fmt.Errorf("clientes: escanear cliente: %w", err)
		}
		items = append(items, catclient.Cliente{
			Id:     id,
			Nombre: nombre,
			Estado: catclient.ClienteEstado(estado),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clientes: filas de cliente: %w", err)
	}
	return items, nil
}
