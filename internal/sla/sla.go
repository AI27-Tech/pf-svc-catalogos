package sla

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

// Comandos es la superficie que el repositorio necesita del pool: leer
// (Query/QueryRow) y abrir transacciones (Begin), porque crear/actualizar un SLA
// y publicar `catalogo.actualizado` deben comprometerse juntos (P17). La
// implementa *pgxpool.Pool. Se declara como interfaz para sustituirla en pruebas.
type Comandos interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Repositorio lee y escribe la versión vigente de los SLA, siempre versionando
// por vigencia (nunca sobrescribe; P8). El SLA es INFORMATIVO en V1 (DEC-022 #4,
// confirmado por Max en DEC-025): se registra, no se vigila ni alerta; ningún
// flujo de E1 lo consume para bloquear ni condicionar el servicio. Existe para
// lectura administrativa y exportación probatoria (P8).
type Repositorio struct {
	db  Comandos
	pub *outbox.Publisher
}

// NewRepositorio construye el repositorio sobre el pool dado. El publicador de
// outbox emite `catalogo.actualizado` dentro de la tx de escritura.
func NewRepositorio(db Comandos) *Repositorio {
	return &Repositorio{db: db, pub: outbox.NewPublisher()}
}

// ErrNoEncontrado indica que el SLA no tiene versión aplicable.
var ErrNoEncontrado = errors.New("sla: no encontrado")

// columnas es el SELECT compartido por las lecturas. El schema Sla (tracer.8)
// expone id, version, contrato_id y descripcion; NO expone la vigencia, pero el
// SLA se versiona por vigencia en la BD igual que el resto de catalogos (0001),
// y esas columnas sostienen la lectura "a una fecha". El orden es el que espera
// escanearSlas. `vigente_desde`/`vigente_hasta` se leen para la lógica de
// vigencia, no para el schema de respuesta.
const columnas = `id, version, contrato_id, descripcion, vigente_desde, vigente_hasta`

// Obtener devuelve un SLA: su versión vigente si aFecha es nil, o la versión que
// regía en aFecha (lectura probatoria P8). Es el criterio de aceptación central
// de E5-T4: `GET /api/v1/sla/{id}?a_fecha=<fecha>` devuelve el que regía en esa
// fecha, no el actual. Devuelve ErrNoEncontrado si no hay versión aplicable.
func (r *Repositorio) Obtener(ctx context.Context, id catclient.Id, aFecha *catclient.MarcaTiempo) (catclient.Sla, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if aFecha == nil {
		rows, err = r.db.Query(ctx, `
			SELECT `+columnas+`
			  FROM catalogos.sla
			 WHERE id = $1 AND vigente_hasta IS NULL`, id)
	} else {
		// La versión vigente a una fecha: vigente_desde <= aFecha y
		// (vigente_hasta es null o aFecha < vigente_hasta).
		rows, err = r.db.Query(ctx, `
			SELECT `+columnas+`
			  FROM catalogos.sla
			 WHERE id = $1
			   AND vigente_desde <= $2
			   AND (vigente_hasta IS NULL OR $2 < vigente_hasta)`, id, *aFecha)
	}
	if err != nil {
		return catclient.Sla{}, fmt.Errorf("sla: obtener: %w", err)
	}
	defer rows.Close()

	items, err := escanearSlas(rows)
	if err != nil {
		return catclient.Sla{}, err
	}
	if len(items) == 0 {
		return catclient.Sla{}, ErrNoEncontrado
	}
	return items[0], nil
}

// Crear da de alta un SLA: INSERT de la versión 1 y emisión de
// `catalogo.actualizado`, ambos en la MISMA transacción (P17). El servicio
// asigna id y versión. El índice único parcial garantiza un solo SLA vigente por
// contrato (un_sla_vigente_por_contrato): un alta duplicada para el mismo
// contrato choca contra él y el caso de uso la traduce a conflicto.
func (r *Repositorio) Crear(ctx context.Context, e catclient.SlaEntrada) (catclient.Sla, error) {
	id := uuid.New()
	var creado catclient.Sla
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.sla
			  (id, version, contrato_id, descripcion)
			VALUES ($1, 1, $2, $3)
			RETURNING `+columnas,
			id, e.ContratoId, e.Descripcion)
		if err != nil {
			return fmt.Errorf("insertar sla: %w", err)
		}
		defer rows.Close()
		items, err := escanearSlas(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		creado = items[0]
		// Evento en la misma tx (P17).
		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "sla", EntidadID: id.String(), Version: 1,
		})
	})
	if err != nil {
		return catclient.Sla{}, fmt.Errorf("sla: crear: %w", err)
	}
	return creado, nil
}

// Actualizar modifica un SLA creando una NUEVA versión: cierra la vigencia de la
// versión actual, inserta la nueva (version+1) con los campos cambiados, y emite
// `catalogo.actualizado`, todo en una sola transacción (P17). No sobrescribe
// (P8): la versión anterior sigue consultable con aFecha. Devuelve el SLA con su
// nueva versión. ErrNoEncontrado si no hay versión vigente.
func (r *Repositorio) Actualizar(ctx context.Context, id catclient.Id, c catclient.SlaCambios, ahora time.Time) (catclient.Sla, error) {
	var nuevo catclient.Sla
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// Leer la versión vigente (bloqueada) para componer la nueva.
		var (
			versionActual int
			contratoID    uuid.UUID
			descripcion   *string
		)
		err := tx.QueryRow(ctx, `
			SELECT version, contrato_id, descripcion
			  FROM catalogos.sla
			 WHERE id = $1 AND vigente_hasta IS NULL
			 FOR UPDATE`, id).Scan(&versionActual, &contratoID, &descripcion)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoEncontrado
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
			UPDATE catalogos.sla SET vigente_hasta = $1
			 WHERE id = $2 AND version = $3`, ahora, id, versionActual); err != nil {
			return fmt.Errorf("cerrar vigencia: %w", err)
		}

		// Insertar la nueva versión, vigente desde ahora.
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.sla
			  (id, version, contrato_id, descripcion, vigente_desde)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+columnas,
			id, nuevaVersion, contratoID, descripcion, ahora)
		if err != nil {
			return fmt.Errorf("insertar nueva versión: %w", err)
		}
		defer rows.Close()
		items, err := escanearSlas(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		nuevo = items[0]

		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: "sla", EntidadID: id.String(), Version: nuevaVersion,
		})
	})
	if err != nil {
		if errors.Is(err, ErrNoEncontrado) {
			return catclient.Sla{}, err
		}
		return catclient.Sla{}, fmt.Errorf("sla: actualizar: %w", err)
	}
	return nuevo, nil
}

// escanearSlas mapea las filas (con columnas = `columnas`) a []Sla. La vigencia
// se lee para la lógica de versionado pero NO viaja en el schema Sla (tracer.8
// no la expone en la respuesta); se descarta tras el Scan.
func escanearSlas(rows pgx.Rows) ([]catclient.Sla, error) {
	var out []catclient.Sla
	for rows.Next() {
		var (
			id            catclient.Id
			version       int
			contratoID    catclient.Id
			descripcion   *string
			vigenciaDesde time.Time  // leída para la lógica de vigencia, no expuesta
			vigenciaHasta *time.Time // idem
		)
		if err := rows.Scan(&id, &version, &contratoID, &descripcion, &vigenciaDesde, &vigenciaHasta); err != nil {
			return nil, fmt.Errorf("sla: escanear: %w", err)
		}
		_, _ = vigenciaDesde, vigenciaHasta // el schema de respuesta no lleva vigencia
		v := version
		out = append(out, catclient.Sla{
			Id:          id,
			Version:     &v,
			ContratoId:  contratoID,
			Descripcion: descripcion,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sla: filas: %w", err)
	}
	return out, nil
}
