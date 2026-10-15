package custodios

import (
	"context"
	"errors"
	"fmt"
	"time"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
	"github.com/AI27-Tech/pf-svc-catalogos/internal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// pagina es el tamaño de página del listado por cursor.
const pagina = 50

// Consultas es la superficie mínima de LECTURA que el sub-módulo necesita de un
// pool: Query y QueryRow. Se declara como interfaz para poder sustituirla en
// pruebas sin una base real.
type Consultas interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Comandos es la superficie de ESCRITURA: además de leer, abre transacciones
// (Begin), porque crear/actualizar un custodio y publicar `catalogo.actualizado`
// deben comprometerse juntos (P17). La implementa *pgxpool.Pool.
type Comandos interface {
	Consultas
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Repositorio lee y escribe la versión vigente de los custodios, siempre
// versionando por vigencia (nunca sobrescribe; P8).
type Repositorio struct {
	db  Comandos
	pub *outbox.Publisher
}

// NewRepositorio construye el repositorio sobre el pool dado. El publicador de
// outbox se usa para emitir `catalogo.actualizado` dentro de la tx de escritura.
func NewRepositorio(db Comandos) *Repositorio {
	return &Repositorio{db: db, pub: outbox.NewPublisher()}
}

// ErrNoEncontrado indica que el custodio no tiene versión aplicable.
var ErrNoEncontrado = errors.New("custodios: no encontrado")

// columnas es el SELECT compartido por las lecturas. Incluye version y vigencia
// (C6): el contrato expone en el schema Custodio `vigencia_desde`/
// `vigencia_hasta`, pero en la BD las columnas se llaman `vigente_desde`/
// `vigente_hasta` (igual que cliente/contrato/sla/vehiculo en 0001/0003). El
// orden de las columnas es el que espera escanearCustodios.
//
// A diferencia de vehiculo, el custodio NO tiene cliente_id: la persona no
// pertenece a un cliente. `usuario_id` es TEXT en la BD (identificador del
// usuario de identidad, NUNCA una FK entre schemas, P1); el mapeo a UUID del
// contrato lo hace escanearCustodios.
const columnas = `id, version, nombre, telefono, estado, usuario_id, vigente_desde, vigente_hasta`

// Listar devuelve una página de custodios en su versión vigente, paginada por
// cursor. cursor nil arranca desde el principio.
func (r *Repositorio) Listar(ctx context.Context, cursor *catclient.Cursor) (catclient.PaginaCustodios, error) {
	desde := ""
	if cursor != nil {
		desde = *cursor
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+columnas+`
		  FROM catalogos.custodio
		 WHERE vigente_hasta IS NULL
		   AND ($1 = '' OR id > $1::uuid)
		 ORDER BY id
		 LIMIT $2`, desde, pagina+1)
	if err != nil {
		return catclient.PaginaCustodios{}, fmt.Errorf("custodios: listar: %w", err)
	}
	defer rows.Close()

	items, err := escanearCustodios(rows)
	if err != nil {
		return catclient.PaginaCustodios{}, err
	}

	var siguiente *string
	if len(items) > pagina {
		ultimo := items[pagina-1].Id.String()
		siguiente = &ultimo
		items = items[:pagina]
	}
	return catclient.PaginaCustodios{Items: items, CursorSiguiente: siguiente}, nil
}

// ListarPorLote devuelve los custodios vigentes cuyos id están en ids, en un
// solo viaje (C4): evita el patrón N+1 de los consumidores. El orden del
// resultado no está garantizado; el consumidor empareja por id.
func (r *Repositorio) ListarPorLote(ctx context.Context, ids []catclient.Id) (catclient.PaginaCustodios, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+columnas+`
		  FROM catalogos.custodio
		 WHERE vigente_hasta IS NULL AND id = ANY($1)
		 ORDER BY id`, ids)
	if err != nil {
		return catclient.PaginaCustodios{}, fmt.Errorf("custodios: lote: %w", err)
	}
	defer rows.Close()

	items, err := escanearCustodios(rows)
	if err != nil {
		return catclient.PaginaCustodios{}, err
	}
	// El lote no se pagina: cursor_siguiente es null (contrato).
	return catclient.PaginaCustodios{Items: items, CursorSiguiente: nil}, nil
}

// Obtener devuelve un custodio: su versión vigente si aFecha es nil, o la versión
// que regía en aFecha (C9, lectura probatoria P8). Devuelve ErrNoEncontrado si
// no hay versión aplicable.
func (r *Repositorio) Obtener(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (catclient.Custodio, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if aFecha == nil {
		rows, err = r.db.Query(ctx, `
			SELECT `+columnas+`
			  FROM catalogos.custodio
			 WHERE id = $1 AND vigente_hasta IS NULL`, id)
	} else {
		// La versión vigente a una fecha: vigente_desde <= aFecha y
		// (vigente_hasta es null o aFecha < vigente_hasta).
		rows, err = r.db.Query(ctx, `
			SELECT `+columnas+`
			  FROM catalogos.custodio
			 WHERE id = $1
			   AND vigente_desde <= $2
			   AND (vigente_hasta IS NULL OR $2 < vigente_hasta)`, id, *aFecha)
	}
	if err != nil {
		return catclient.Custodio{}, fmt.Errorf("custodios: obtener: %w", err)
	}
	defer rows.Close()

	items, err := escanearCustodios(rows)
	if err != nil {
		return catclient.Custodio{}, err
	}
	if len(items) == 0 {
		return catclient.Custodio{}, ErrNoEncontrado
	}
	return items[0], nil
}

// Aptitud evalúa la aptitud INFORMATIVA de un custodio (DEC-055): en V1 solo
// mira el `estado` (activo), nunca bloquea.
//
// NOTA DE CONTRATO (pendiente): el contrato declara `cliente_id` obligatorio en
// la ruta de aptitud, por simetría con vehiculo. Pero el custodio NO pertenece a
// un cliente (su tabla no tiene cliente_id), así que aquí NO se filtra por él: el
// parámetro se acepta y se ignora. Y a diferencia de vehiculo, el custodio aún
// no tiene tabla de documentos (E5-HU-022 lo dejó "Por confirmar"), así que la
// aptitud no evalúa documentos vencidos. Ambas cosas quedan como pendientes de
// Operación/Lead; sin su decisión, la vía fiel a V1 es aptitud por estado.
func (r *Repositorio) Aptitud(ctx context.Context, custodioID catclient.Id) (catclient.Aptitud, error) {
	var estado string
	err := r.db.QueryRow(ctx, `
		SELECT estado
		  FROM catalogos.custodio
		 WHERE id = $1 AND vigente_hasta IS NULL`,
		custodioID).Scan(&estado)
	if errors.Is(err, pgx.ErrNoRows) {
		return catclient.Aptitud{}, ErrNoEncontrado
	}
	if err != nil {
		return catclient.Aptitud{}, fmt.Errorf("custodios: aptitud: %w", err)
	}

	motivos := make([]string, 0, 1)
	if estado != string(catclient.CustodioEstadoActivo) {
		motivos = append(motivos, "custodio inactivo")
	}

	ap := catclient.Aptitud{RecursoId: custodioID, Apto: len(motivos) == 0}
	if len(motivos) > 0 {
		ap.Motivos = &motivos
	}
	return ap, nil
}

// Crear da de alta un custodio: INSERT de la versión 1 y emisión de
// `catalogo.actualizado`, ambos en la MISMA transacción (P17). El servicio
// asigna id y versión. Devuelve el custodio creado.
func (r *Repositorio) Crear(ctx context.Context, e catclient.CustodioEntrada) (catclient.Custodio, error) {
	id := uuid.New()
	var creado catclient.Custodio
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.custodio
			  (id, version, nombre, telefono, estado, usuario_id)
			VALUES ($1, 1, $2, $3, $4, $5)
			RETURNING `+columnas,
			id, e.Nombre, e.Telefono, string(e.Estado), textoUUID(e.UsuarioId))
		if err != nil {
			return fmt.Errorf("insertar custodio: %w", err)
		}
		defer rows.Close()
		items, err := escanearCustodios(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		creado = items[0]
		// Evento en la misma tx (P17).
		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "custodio", EntidadID: id.String(), Version: 1,
		})
	})
	if err != nil {
		return catclient.Custodio{}, fmt.Errorf("custodios: crear: %w", err)
	}
	return creado, nil
}

// Actualizar modifica un custodio creando una NUEVA versión: cierra la vigencia
// de la versión actual, inserta la nueva (version+1) con los campos cambiados, y
// emite `catalogo.actualizado`, todo en una sola transacción (P17). No
// sobrescribe (P8). Devuelve el custodio con su nueva versión. ErrNoEncontrado
// si no hay versión vigente.
func (r *Repositorio) Actualizar(ctx context.Context, id catclient.Id, c catclient.CustodioCambios, ahora time.Time) (catclient.Custodio, error) {
	var nuevo catclient.Custodio
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// Leer la versión vigente (bloqueada) para componer la nueva.
		var (
			versionActual     int
			nombre, estado    string
			telefono, usuario *string
		)
		err := tx.QueryRow(ctx, `
			SELECT version, nombre, telefono, estado, usuario_id
			  FROM catalogos.custodio
			 WHERE id = $1 AND vigente_hasta IS NULL
			 FOR UPDATE`, id).Scan(&versionActual, &nombre, &telefono, &estado, &usuario)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoEncontrado
		}
		if err != nil {
			return fmt.Errorf("leer versión vigente: %w", err)
		}

		// Aplicar los cambios presentes sobre los valores actuales.
		if c.Nombre != nil {
			nombre = *c.Nombre
		}
		if c.Estado != nil {
			estado = string(*c.Estado)
		}
		if c.Telefono != nil {
			telefono = c.Telefono
		}
		if c.UsuarioId != nil {
			usuario = textoUUID(c.UsuarioId)
		}
		nuevaVersion := versionActual + 1

		// Cerrar la vigencia de la versión actual (único UPDATE permitido).
		if _, err := tx.Exec(ctx, `
			UPDATE catalogos.custodio SET vigente_hasta = $1
			 WHERE id = $2 AND version = $3`, ahora, id, versionActual); err != nil {
			return fmt.Errorf("cerrar vigencia: %w", err)
		}

		// Insertar la nueva versión, vigente desde ahora.
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.custodio
			  (id, version, nombre, telefono, estado, usuario_id, vigente_desde)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+columnas,
			id, nuevaVersion, nombre, telefono, estado, usuario, ahora)
		if err != nil {
			return fmt.Errorf("insertar nueva versión: %w", err)
		}
		defer rows.Close()
		items, err := escanearCustodios(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		nuevo = items[0]

		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "custodio", EntidadID: id.String(), Version: nuevaVersion,
		})
	})
	if err != nil {
		if errors.Is(err, ErrNoEncontrado) {
			return catclient.Custodio{}, err
		}
		return catclient.Custodio{}, fmt.Errorf("custodios: actualizar: %w", err)
	}
	return nuevo, nil
}

// textoUUID convierte el UUID opcional del contrato a la representación TEXT que
// usa la columna usuario_id (identificador, no FK; P1). nil ⇒ nil (null en BD).
func textoUUID(u *openapi_types.UUID) *string {
	if u == nil {
		return nil
	}
	s := u.String()
	return &s
}

// escanearCustodios mapea las filas (con columnas = `columnas`) a []Custodio.
// Hace el mapeo BD→contrato: usuario_id es TEXT en la BD y *uuid.UUID en el
// contrato (identificador, no FK; P1); un valor no vacío se parsea a UUID.
func escanearCustodios(rows pgx.Rows) ([]catclient.Custodio, error) {
	var out []catclient.Custodio
	for rows.Next() {
		var (
			id            catclient.Id
			version       int
			nombre        string
			telefono      *string
			estado        string
			usuarioID     *string
			vigenciaDesde time.Time
			vigenciaHasta *time.Time
		)
		if err := rows.Scan(&id, &version, &nombre, &telefono, &estado, &usuarioID, &vigenciaDesde, &vigenciaHasta); err != nil {
			return nil, fmt.Errorf("custodios: escanear: %w", err)
		}
		v := version
		vd := vigenciaDesde
		cst := catclient.Custodio{
			Id:            id,
			Version:       &v,
			Nombre:        nombre,
			Telefono:      telefono,
			Estado:        catclient.CustodioEstado(estado),
			VigenciaDesde: &vd,
			VigenciaHasta: vigenciaHasta,
		}
		if usuarioID != nil && *usuarioID != "" {
			u, err := uuid.Parse(*usuarioID)
			if err != nil {
				return nil, fmt.Errorf("custodios: usuario_id no es un UUID válido: %w", err)
			}
			uid := openapi_types.UUID(u)
			cst.UsuarioId = &uid
		}
		out = append(out, cst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("custodios: filas: %w", err)
	}
	return out, nil
}
