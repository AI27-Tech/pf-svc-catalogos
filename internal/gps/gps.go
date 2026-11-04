// Package gps lee el catálogo de dispositivos GPS (DEC-047 #2, DEC-104 #5) que
// la ficha técnica del viaje referencia por id. Solo lectura en V1: la carga de
// los dispositivos es trabajo de datos de Operación.
package gps

import (
	"context"
	"errors"
	"fmt"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// pagina es el tamaño de página del listado por cursor (igual que clientes).
const pagina = 50

// Consultas es la superficie de lectura que el repositorio necesita del pool.
type Consultas interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Repositorio lee catalogos.gps.
type Repositorio struct {
	db Consultas
}

// ErrCursorInvalido indica un cursor que no es el id de un dispositivo.
var ErrCursorInvalido = errors.New("gps: cursor inválido")

// NewRepositorio construye el repositorio sobre el pool de svc_catalogos.
func NewRepositorio(db Consultas) *Repositorio {
	return &Repositorio{db: db}
}

// Listar devuelve una página de dispositivos ACTIVOS ordenados por id: el
// selector de la ficha solo ofrece activos y el filtro es del servidor (DEC-104
// #5). El cursor es el id del último de la página anterior; nil arranca desde el
// principio. ErrCursorInvalido si el cursor no es un id.
func (r *Repositorio) Listar(ctx context.Context, cursor *catclient.Cursor) (catclient.PaginaGps, error) {
	desde := ""
	if cursor != nil {
		if _, err := uuid.Parse(*cursor); err != nil {
			return catclient.PaginaGps{}, ErrCursorInvalido
		}
		desde = *cursor
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, identificador, activo
		  FROM catalogos.gps
		 WHERE activo
		   AND ($1 = '' OR id > $1::uuid)
		 ORDER BY id
		 LIMIT $2`, desde, pagina+1)
	if err != nil {
		return catclient.PaginaGps{}, fmt.Errorf("gps: listar: %w", err)
	}
	defer rows.Close()

	items := []catclient.DispositivoGps{}
	for rows.Next() {
		var d catclient.DispositivoGps
		if err := rows.Scan(&d.Id, &d.Identificador, &d.Activo); err != nil {
			return catclient.PaginaGps{}, fmt.Errorf("gps: escanear: %w", err)
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return catclient.PaginaGps{}, fmt.Errorf("gps: iterar: %w", err)
	}

	var siguiente *string
	if len(items) > pagina {
		ultimo := items[pagina-1].Id.String()
		siguiente = &ultimo
		items = items[:pagina]
	}
	return catclient.PaginaGps{Items: items, CursorSiguiente: siguiente}, nil
}
