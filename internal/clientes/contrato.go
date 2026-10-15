package clientes

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

// Comandos es la superficie que el repositorio de contrato necesita del pool:
// leer (Query/QueryRow) y abrir transacciones (Begin), porque crear/actualizar
// un contrato y publicar `catalogo.actualizado` deben comprometerse juntos
// (P17). La implementa *pgxpool.Pool. Se declara como interfaz para poder
// sustituirla en pruebas.
type Comandos interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// RepositorioContrato lee y escribe la versión vigente de los contratos, siempre
// versionando por vigencia (nunca sobrescribe; P8). El contrato es INFORMATIVO
// en V1 (DEC-022 #4 / DEC-025): ningún flujo de E1 lo consume para bloquear ni
// alertar; existe para lectura administrativa y exportación probatoria (P8).
type RepositorioContrato struct {
	db  Comandos
	pub *outbox.Publisher
}

// NewRepositorioContrato construye el repositorio sobre el pool dado. El
// publicador de outbox emite `catalogo.actualizado` dentro de la tx de escritura.
func NewRepositorioContrato(db Comandos) *RepositorioContrato {
	return &RepositorioContrato{db: db, pub: outbox.NewPublisher()}
}

// ErrContratoNoEncontrado indica que el contrato no tiene versión aplicable.
var ErrContratoNoEncontrado = errors.New("clientes: contrato no encontrado")

// columnasContrato es el SELECT compartido por las lecturas. En la BD las
// columnas de vigencia se llaman `vigente_desde`/`vigente_hasta` (igual que el
// resto de catalogos en 0001); el schema Contrato las expone como
// `vigencia_desde`/`vigencia_hasta`. El orden es el que espera escanearContratos.
//
// `descripcion` se SELECCIONA para poder arrastrarla en el PATCH (componer la
// nueva versión), pero NO viaja en el schema de lectura Contrato (tracer.8): el
// contrato la declara solo en ContratoEntrada/ContratoCambios (entrada), no en
// la respuesta. escanearContratos la lee a un descarte. `estado` tampoco viaja:
// el contrato no lo expone en V1 (0008 lo relajó a NULL).
const columnasContrato = `id, version, cliente_id, descripcion, vigente_desde, vigente_hasta`

// Obtener devuelve un contrato: su versión vigente si aFecha es nil, o la versión
// que regía en aFecha (lectura probatoria P8, la que la exportación necesita).
// Devuelve ErrContratoNoEncontrado si no hay versión aplicable.
func (r *RepositorioContrato) Obtener(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (catclient.Contrato, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if aFecha == nil {
		rows, err = r.db.Query(ctx, `
			SELECT `+columnasContrato+`
			  FROM catalogos.contrato
			 WHERE id = $1 AND vigente_hasta IS NULL`, id)
	} else {
		// La versión vigente a una fecha: vigente_desde <= aFecha y
		// (vigente_hasta es null o aFecha < vigente_hasta).
		rows, err = r.db.Query(ctx, `
			SELECT `+columnasContrato+`
			  FROM catalogos.contrato
			 WHERE id = $1
			   AND vigente_desde <= $2
			   AND (vigente_hasta IS NULL OR $2 < vigente_hasta)`, id, *aFecha)
	}
	if err != nil {
		return catclient.Contrato{}, fmt.Errorf("clientes: obtener contrato: %w", err)
	}
	defer rows.Close()

	items, err := escanearContratos(rows)
	if err != nil {
		return catclient.Contrato{}, err
	}
	if len(items) == 0 {
		return catclient.Contrato{}, ErrContratoNoEncontrado
	}
	return items[0], nil
}

// Crear da de alta un contrato: INSERT de la versión 1 y emisión de
// `catalogo.actualizado`, ambos en la MISMA transacción (P17). El servicio
// asigna id y versión. El único índice único parcial garantiza un solo contrato
// vigente por cliente (un_contrato_vigente_por_cliente): un alta duplicada para
// el mismo cliente choca contra él y el caso de uso la traduce a conflicto.
func (r *RepositorioContrato) Crear(ctx context.Context, e catclient.ContratoEntrada) (catclient.Contrato, error) {
	id := uuid.New()
	var creado catclient.Contrato
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.contrato
			  (id, version, cliente_id, descripcion)
			VALUES ($1, 1, $2, $3)
			RETURNING `+columnasContrato,
			id, e.ClienteId, e.Descripcion)
		if err != nil {
			return fmt.Errorf("insertar contrato: %w", err)
		}
		defer rows.Close()
		items, err := escanearContratos(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		creado = items[0]
		// Evento en la misma tx (P17).
		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "contrato", EntidadID: id.String(), Version: 1,
		})
	})
	if err != nil {
		return catclient.Contrato{}, fmt.Errorf("clientes: crear contrato: %w", err)
	}
	return creado, nil
}

// Actualizar modifica un contrato creando una NUEVA versión: cierra la vigencia
// de la versión actual, inserta la nueva (version+1) con los campos cambiados, y
// emite `catalogo.actualizado`, todo en una sola transacción (P17). No
// sobrescribe (P8): la versión anterior sigue consultable con aFecha. Devuelve
// el contrato con su nueva versión. ErrContratoNoEncontrado si no hay versión
// vigente.
func (r *RepositorioContrato) Actualizar(ctx context.Context, id catclient.Id, c catclient.ContratoCambios, ahora time.Time) (catclient.Contrato, error) {
	var nuevo catclient.Contrato
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// Leer la versión vigente (bloqueada) para componer la nueva.
		var (
			versionActual int
			clienteID     uuid.UUID
			descripcion   *string
		)
		err := tx.QueryRow(ctx, `
			SELECT version, cliente_id, descripcion
			  FROM catalogos.contrato
			 WHERE id = $1 AND vigente_hasta IS NULL
			 FOR UPDATE`, id).Scan(&versionActual, &clienteID, &descripcion)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrContratoNoEncontrado
		}
		if err != nil {
			return fmt.Errorf("leer versión vigente: %w", err)
		}

		// Aplicar los cambios presentes sobre los valores actuales.
		if c.Descripcion != nil {
			descripcion = c.Descripcion
		}
		nuevaVersion := versionActual + 1

		// Cerrar la vigencia de la versión actual (único UPDATE permitido).
		if _, err := tx.Exec(ctx, `
			UPDATE catalogos.contrato SET vigente_hasta = $1
			 WHERE id = $2 AND version = $3`, ahora, id, versionActual); err != nil {
			return fmt.Errorf("cerrar vigencia: %w", err)
		}

		// Insertar la nueva versión, vigente desde ahora.
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.contrato
			  (id, version, cliente_id, descripcion, vigente_desde)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+columnasContrato,
			id, nuevaVersion, clienteID, descripcion, ahora)
		if err != nil {
			return fmt.Errorf("insertar nueva versión: %w", err)
		}
		defer rows.Close()
		items, err := escanearContratos(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		nuevo = items[0]

		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "contrato", EntidadID: id.String(), Version: nuevaVersion,
		})
	})
	if err != nil {
		if errors.Is(err, ErrContratoNoEncontrado) {
			return catclient.Contrato{}, err
		}
		return catclient.Contrato{}, fmt.Errorf("clientes: actualizar contrato: %w", err)
	}
	return nuevo, nil
}

// escanearContratos mapea las filas (con columnas = `columnasContrato`) a
// []Contrato. La BD usa `vigente_desde`/`vigente_hasta`; el contrato los expone
// como `vigencia_desde`/`vigencia_hasta`.
func escanearContratos(rows pgx.Rows) ([]catclient.Contrato, error) {
	var out []catclient.Contrato
	for rows.Next() {
		var (
			id            catclient.Id
			version       int
			clienteID     catclient.Id
			descripcion   *string // leída pero no expuesta en el schema de lectura
			vigenciaDesde time.Time
			vigenciaHasta *time.Time
		)
		if err := rows.Scan(&id, &version, &clienteID, &descripcion, &vigenciaDesde, &vigenciaHasta); err != nil {
			return nil, fmt.Errorf("clientes: escanear contrato: %w", err)
		}
		_ = descripcion // el contrato no la expone en la respuesta (tracer.8)
		v := version
		vd := vigenciaDesde
		out = append(out, catclient.Contrato{
			Id:            id,
			Version:       &v,
			ClienteId:     clienteID,
			VigenciaDesde: vd,
			VigenciaHasta: vigenciaHasta,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clientes: filas de contrato: %w", err)
	}
	return out, nil
}
