package vehiculos

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
// (Begin), porque crear/actualizar un vehículo y publicar `catalogo.actualizado`
// deben comprometerse juntos (P17). La implementa *pgxpool.Pool.
type Comandos interface {
	Consultas
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Repositorio lee y escribe la versión vigente de los vehículos y sus
// documentos, siempre versionando por vigencia (nunca sobrescribe; P8).
type Repositorio struct {
	db  Comandos
	pub *outbox.Publisher
}

// NewRepositorio construye el repositorio sobre el pool dado. El publicador de
// outbox se usa para emitir `catalogo.actualizado` dentro de la tx de escritura.
func NewRepositorio(db Comandos) *Repositorio {
	return &Repositorio{db: db, pub: outbox.NewPublisher()}
}

// ErrNoEncontrado indica que el vehículo no tiene versión aplicable.
var ErrNoEncontrado = errors.New("vehiculos: no encontrado")

// columnas es el SELECT compartido por las lecturas. Incluye version y vigencia
// (C6): el contrato tracer.6 los expone en el schema Vehiculo como
// `vigencia_desde`/`vigencia_hasta`, pero en la BD las columnas se llaman
// `vigente_desde`/`vigente_hasta` (igual que cliente/contrato/sla en 0001). El
// orden de las columnas es el que espera escanearVehiculos.
const columnas = `id, version, tipo, economico, placas, estado, vigente_desde, vigente_hasta`

// Listar devuelve una página de vehículos en su versión vigente, paginada por
// cursor. cursor nil arranca desde el principio.
func (r *Repositorio) Listar(ctx context.Context, cursor *catclient.Cursor) (catclient.PaginaVehiculos, error) {
	desde := ""
	if cursor != nil {
		desde = *cursor
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+columnas+`
		  FROM catalogos.vehiculo
		 WHERE vigente_hasta IS NULL
		   AND ($1 = '' OR id > $1::uuid)
		 ORDER BY id
		 LIMIT $2`, desde, pagina+1)
	if err != nil {
		return catclient.PaginaVehiculos{}, fmt.Errorf("vehiculos: listar: %w", err)
	}
	defer rows.Close()

	items, err := escanearVehiculos(rows)
	if err != nil {
		return catclient.PaginaVehiculos{}, err
	}

	var siguiente *string
	if len(items) > pagina {
		ultimo := items[pagina-1].Id.String()
		siguiente = &ultimo
		items = items[:pagina]
	}
	return catclient.PaginaVehiculos{Items: items, CursorSiguiente: siguiente}, nil
}

// ListarPorLote devuelve los vehículos vigentes cuyos id están en ids, en un
// solo viaje (C4): evita el patrón N+1 de los consumidores. El orden del
// resultado no está garantizado; el consumidor empareja por id.
func (r *Repositorio) ListarPorLote(ctx context.Context, ids []catclient.Id) (catclient.PaginaVehiculos, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+columnas+`
		  FROM catalogos.vehiculo
		 WHERE vigente_hasta IS NULL AND id = ANY($1)
		 ORDER BY id`, ids)
	if err != nil {
		return catclient.PaginaVehiculos{}, fmt.Errorf("vehiculos: lote: %w", err)
	}
	defer rows.Close()

	items, err := escanearVehiculos(rows)
	if err != nil {
		return catclient.PaginaVehiculos{}, err
	}
	// El lote no se pagina: cursor_siguiente es null (contrato).
	return catclient.PaginaVehiculos{Items: items, CursorSiguiente: nil}, nil
}

// Obtener devuelve un vehículo: su versión vigente si aFecha es nil, o la versión
// que regía en aFecha (C9, lectura probatoria P8). Devuelve ErrNoEncontrado si
// no hay versión aplicable.
func (r *Repositorio) Obtener(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (catclient.Vehiculo, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if aFecha == nil {
		rows, err = r.db.Query(ctx, `
			SELECT `+columnas+`
			  FROM catalogos.vehiculo
			 WHERE id = $1 AND vigente_hasta IS NULL`, id)
	} else {
		// La versión vigente a una fecha: vigencia_desde <= aFecha y
		// (vigente_hasta es null o aFecha < vigente_hasta).
		rows, err = r.db.Query(ctx, `
			SELECT `+columnas+`
			  FROM catalogos.vehiculo
			 WHERE id = $1
			   AND vigente_desde <= $2
			   AND (vigente_hasta IS NULL OR $2 < vigente_hasta)`, id, *aFecha)
	}
	if err != nil {
		return catclient.Vehiculo{}, fmt.Errorf("vehiculos: obtener: %w", err)
	}
	defer rows.Close()

	items, err := escanearVehiculos(rows)
	if err != nil {
		return catclient.Vehiculo{}, err
	}
	if len(items) == 0 {
		return catclient.Vehiculo{}, ErrNoEncontrado
	}
	return items[0], nil
}

// Aptitud evalúa la aptitud INFORMATIVA de un vehículo para el contrato/SLA de
// un cliente (DEC-055): activo y con documentos vigentes. NUNCA bloquea.
func (r *Repositorio) Aptitud(ctx context.Context, vehiculoID catclient.Id, clienteID catclient.Id, ahora time.Time) (catclient.Aptitud, error) {
	var estado string
	err := r.db.QueryRow(ctx, `
		SELECT estado
		  FROM catalogos.vehiculo
		 WHERE id = $1 AND cliente_id = $2 AND vigente_hasta IS NULL`,
		vehiculoID, clienteID).Scan(&estado)
	if errors.Is(err, pgx.ErrNoRows) {
		return catclient.Aptitud{}, ErrNoEncontrado
	}
	if err != nil {
		return catclient.Aptitud{}, fmt.Errorf("vehiculos: aptitud: %w", err)
	}

	motivos := make([]string, 0, 2)
	if estado != string(catclient.Activa) {
		motivos = append(motivos, "vehículo inactivo")
	}
	var vencidos int
	err = r.db.QueryRow(ctx, `
		SELECT count(*)
		  FROM catalogos.vehiculo_documento
		 WHERE vehiculo_id = $1 AND vigente_hasta IS NULL
		   AND vence_el IS NOT NULL AND vence_el < $2`,
		vehiculoID, ahora).Scan(&vencidos)
	if err != nil {
		return catclient.Aptitud{}, fmt.Errorf("vehiculos: aptitud (documentos): %w", err)
	}
	if vencidos > 0 {
		motivos = append(motivos, "documento vencido")
	}

	ap := catclient.Aptitud{RecursoId: vehiculoID, Apto: len(motivos) == 0}
	if len(motivos) > 0 {
		ap.Motivos = &motivos
	}
	return ap, nil
}

// Crear da de alta un vehículo: INSERT de la versión 1 y emisión de
// `catalogo.actualizado`, ambos en la MISMA transacción (P17). El servicio
// asigna id y versión. Devuelve el vehículo creado.
func (r *Repositorio) Crear(ctx context.Context, e catclient.VehiculoEntrada, clienteID catclient.Id) (catclient.Vehiculo, error) {
	id := uuid.New()
	var creado catclient.Vehiculo
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.vehiculo
			  (id, version, tipo, economico, placas, estado, cliente_id)
			VALUES ($1, 1, $2, $3, $4, $5, $6)
			RETURNING `+columnas,
			id, string(e.Tipo), e.Economico, e.Placas, string(e.Estado), clienteID)
		if err != nil {
			return fmt.Errorf("insertar vehículo: %w", err)
		}
		defer rows.Close()
		items, err := escanearVehiculos(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		creado = items[0]
		// Evento en la misma tx (P17).
		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "vehiculo", EntidadID: id.String(), Version: 1,
		})
	})
	if err != nil {
		return catclient.Vehiculo{}, fmt.Errorf("vehiculos: crear: %w", err)
	}
	return creado, nil
}

// Actualizar modifica un vehículo creando una NUEVA versión: cierra la vigencia
// de la versión actual, inserta la nueva (version+1) con los campos cambiados, y
// emite `catalogo.actualizado`, todo en una sola transacción (P17). No
// sobrescribe (P8). Devuelve el vehículo con su nueva versión. ErrNoEncontrado
// si no hay versión vigente.
func (r *Repositorio) Actualizar(ctx context.Context, id catclient.Id, c catclient.VehiculoCambios, ahora time.Time) (catclient.Vehiculo, error) {
	var nuevo catclient.Vehiculo
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// Leer la versión vigente (bloqueada) para componer la nueva.
		var (
			versionActual     int
			tipo, estado      string
			economico, placas *string
			clienteID         uuid.UUID
		)
		err := tx.QueryRow(ctx, `
			SELECT version, tipo, economico, placas, estado, cliente_id
			  FROM catalogos.vehiculo
			 WHERE id = $1 AND vigente_hasta IS NULL
			 FOR UPDATE`, id).Scan(&versionActual, &tipo, &economico, &placas, &estado, &clienteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoEncontrado
		}
		if err != nil {
			return fmt.Errorf("leer versión vigente: %w", err)
		}

		// Aplicar los cambios presentes sobre los valores actuales.
		if c.Tipo != nil {
			tipo = string(*c.Tipo)
		}
		if c.Estado != nil {
			estado = string(*c.Estado)
		}
		if c.Economico != nil {
			economico = c.Economico
		}
		if c.Placas != nil {
			placas = c.Placas
		}
		nuevaVersion := versionActual + 1

		// Cerrar la vigencia de la versión actual (único UPDATE permitido).
		if _, err := tx.Exec(ctx, `
			UPDATE catalogos.vehiculo SET vigente_hasta = $1
			 WHERE id = $2 AND version = $3`, ahora, id, versionActual); err != nil {
			return fmt.Errorf("cerrar vigencia: %w", err)
		}

		// Insertar la nueva versión, vigente desde ahora.
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.vehiculo
			  (id, version, tipo, economico, placas, estado, cliente_id, vigente_desde)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING `+columnas,
			id, nuevaVersion, tipo, economico, placas, estado, clienteID, ahora)
		if err != nil {
			return fmt.Errorf("insertar nueva versión: %w", err)
		}
		defer rows.Close()
		items, err := escanearVehiculos(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		nuevo = items[0]

		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "vehiculo", EntidadID: id.String(), Version: nuevaVersion,
		})
	})
	if err != nil {
		if errors.Is(err, ErrNoEncontrado) {
			return catclient.Vehiculo{}, err
		}
		return catclient.Vehiculo{}, fmt.Errorf("vehiculos: actualizar: %w", err)
	}
	return nuevo, nil
}

// escanearVehiculos mapea las filas (con columnas = `columnas`) a []Vehiculo.
func escanearVehiculos(rows pgx.Rows) ([]catclient.Vehiculo, error) {
	var out []catclient.Vehiculo
	for rows.Next() {
		var (
			id            catclient.Id
			version       int
			tipo          string
			economico     *string
			placas        *string
			estado        string
			vigenciaDesde time.Time
			vigenciaHasta *time.Time
		)
		if err := rows.Scan(&id, &version, &tipo, &economico, &placas, &estado, &vigenciaDesde, &vigenciaHasta); err != nil {
			return nil, fmt.Errorf("vehiculos: escanear: %w", err)
		}
		v := version
		vd := vigenciaDesde
		out = append(out, catclient.Vehiculo{
			Id:            id,
			Version:       &v,
			Tipo:          catclient.VehiculoTipo(tipo),
			Economico:     economico,
			Placas:        placas,
			Estado:        catclient.VehiculoEstado(estado),
			VigenciaDesde: &vd,
			VigenciaHasta: vigenciaHasta,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vehiculos: filas: %w", err)
	}
	return out, nil
}
