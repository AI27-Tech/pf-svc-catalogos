package tipos

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

// entidadEvento es el valor de `entidad` de catalogo.actualizado para los cinco
// catálogos de tipo: comparten tabla, así que comparten entidad. Cuál de las
// cinco listas cambió viaja en el subtipo del payload (decisión B2), no aquí.
const entidadEvento = "valor_catalogo"

// Consultas es la superficie mínima de LECTURA que el sub-módulo necesita de un
// pool: Query y QueryRow. Se declara como interfaz para poder sustituirla en
// pruebas sin una base real.
type Consultas interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Comandos es la superficie de ESCRITURA: además de leer, abre transacciones
// (Begin), porque crear/actualizar un valor de catálogo y publicar
// `catalogo.actualizado` deben comprometerse juntos (P17). La implementa
// *pgxpool.Pool.
type Comandos interface {
	Consultas
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Repositorio lee y escribe la versión vigente de los valores de catálogo de
// tipo, siempre versionando por vigencia (nunca sobrescribe; P8).
type Repositorio struct {
	db  Comandos
	pub *outbox.Publisher
}

// NewRepositorio construye el repositorio sobre el pool dado. El publicador de
// outbox se usa para emitir `catalogo.actualizado` dentro de la tx de escritura.
func NewRepositorio(db Comandos) *Repositorio {
	return &Repositorio{db: db, pub: outbox.NewPublisher()}
}

// ErrNoEncontrado indica que el valor no tiene versión vigente.
var ErrNoEncontrado = errors.New("tipos: no encontrado")

// ErrTipoInvalido indica que el `tipo` no es uno de los cinco catálogos de V1.
var ErrTipoInvalido = errors.New("tipos: tipo de catálogo inválido")

// tiposValidos es el conjunto de las cinco listas de V1 (enum TipoCatalogo del
// contrato tracer.8). La validación vive en el caso de uso, no en un CHECK de la
// BD: agregar una lista futura es un valor más del enum, no una migración.
var tiposValidos = map[catclient.TipoCatalogo]struct{}{
	catclient.TiposServicio:    {},
	catclient.LineasTransporte: {},
	catclient.TiposUnidad:      {},
	catclient.TiposCustodia:    {},
	catclient.ZonasRiesgo:      {},
}

// ValidarTipo comprueba que el `tipo` de la ruta es uno de los cinco catálogos
// de V1. Lo expone el caso de uso para rechazar temprano (400) antes de tocar la
// BD. Devuelve ErrTipoInvalido si no lo es.
func ValidarTipo(tipo catclient.TipoCatalogo) error {
	if _, ok := tiposValidos[tipo]; !ok {
		return ErrTipoInvalido
	}
	return nil
}

// columnas es el SELECT compartido por las lecturas. Incluye version para la
// revalidación barata de caché (el contrato lo expone), pero NO las vigencias:
// a diferencia de Vehiculo/Custodio, el schema ValorCatalogo no las expone. Se
// seleccionan igual para que escanearValores pueda ordenar/filtrar, aunque no
// viajen al contrato. El orden es el que espera escanearValores.
const columnas = `id, version, codigo, etiqueta, estado`

// Listar devuelve una página de valores vigentes del catálogo `tipo`, paginada
// por cursor. cursor nil arranca desde el principio. Rechaza un tipo inválido.
func (r *Repositorio) Listar(ctx context.Context, tipo catclient.TipoCatalogo, cursor *catclient.Cursor) (catclient.PaginaValoresCatalogo, error) {
	if err := ValidarTipo(tipo); err != nil {
		return catclient.PaginaValoresCatalogo{}, err
	}
	desde := ""
	if cursor != nil {
		desde = *cursor
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+columnas+`
		  FROM catalogos.valor_catalogo
		 WHERE tipo = $1
		   AND vigente_hasta IS NULL
		   AND ($2 = '' OR id > $2::uuid)
		 ORDER BY id
		 LIMIT $3`, string(tipo), desde, pagina+1)
	if err != nil {
		return catclient.PaginaValoresCatalogo{}, fmt.Errorf("tipos: listar: %w", err)
	}
	defer rows.Close()

	items, err := escanearValores(rows)
	if err != nil {
		return catclient.PaginaValoresCatalogo{}, err
	}

	var siguiente *string
	if len(items) > pagina {
		ultimo := items[pagina-1].Id.String()
		siguiente = &ultimo
		items = items[:pagina]
	}
	return catclient.PaginaValoresCatalogo{Items: items, CursorSiguiente: siguiente}, nil
}

// Crear da de alta un valor en el catálogo `tipo`: INSERT de la versión 1 y
// emisión de `catalogo.actualizado` (con el subtipo = `tipo`, B2), ambos en la
// MISMA transacción (P17). El servicio asigna id y versión. Rechaza un tipo
// inválido. Devuelve el valor creado.
func (r *Repositorio) Crear(ctx context.Context, tipo catclient.TipoCatalogo, e catclient.ValorCatalogoEntrada) (catclient.ValorCatalogo, error) {
	if err := ValidarTipo(tipo); err != nil {
		return catclient.ValorCatalogo{}, err
	}
	id := uuid.New()
	var creado catclient.ValorCatalogo
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.valor_catalogo
			  (id, version, tipo, codigo, etiqueta, estado)
			VALUES ($1, 1, $2, $3, $4, $5)
			RETURNING `+columnas,
			id, string(tipo), e.Codigo, e.Etiqueta, string(e.Estado))
		if err != nil {
			return fmt.Errorf("insertar valor: %w", err)
		}
		defer rows.Close()
		items, err := escanearValores(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		creado = items[0]
		// Evento en la misma tx (P17), con el subtipo = lista (B2).
		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: entidadEvento, EntidadID: id.String(), Version: 1, SubTipo: string(tipo),
		})
	})
	if err != nil {
		return catclient.ValorCatalogo{}, fmt.Errorf("tipos: crear: %w", err)
	}
	return creado, nil
}

// Actualizar modifica un valor creando una NUEVA versión: cierra la vigencia de
// la versión actual, inserta la nueva (version+1) con los campos cambiados, y
// emite `catalogo.actualizado`, todo en una sola transacción (P17). No
// sobrescribe (P8). DESACTIVAR es enviar estado 'inactivo' por esta misma vía:
// la versión vigente anterior queda cerrada pero intacta, así que los registros
// históricos que la usaban siguen leyéndola "a una fecha" (E5-HU-023). Devuelve
// el valor con su nueva versión. ErrNoEncontrado si no hay versión vigente.
func (r *Repositorio) Actualizar(ctx context.Context, tipo catclient.TipoCatalogo, id catclient.Id, c catclient.ValorCatalogoCambios, ahora time.Time) (catclient.ValorCatalogo, error) {
	if err := ValidarTipo(tipo); err != nil {
		return catclient.ValorCatalogo{}, err
	}
	var nuevo catclient.ValorCatalogo
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// Leer la versión vigente (bloqueada) para componer la nueva. Se acota
		// por tipo para que un id de otra lista no se confunda.
		var (
			versionActual            int
			codigo, etiqueta, estado string
		)
		err := tx.QueryRow(ctx, `
			SELECT version, codigo, etiqueta, estado
			  FROM catalogos.valor_catalogo
			 WHERE id = $1 AND tipo = $2 AND vigente_hasta IS NULL
			 FOR UPDATE`, id, string(tipo)).Scan(&versionActual, &codigo, &etiqueta, &estado)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoEncontrado
		}
		if err != nil {
			return fmt.Errorf("leer versión vigente: %w", err)
		}

		// Aplicar los cambios presentes sobre los valores actuales.
		if c.Codigo != nil {
			codigo = *c.Codigo
		}
		if c.Etiqueta != nil {
			etiqueta = *c.Etiqueta
		}
		if c.Estado != nil {
			estado = string(*c.Estado)
		}
		nuevaVersion := versionActual + 1

		// Cerrar la vigencia de la versión actual (único UPDATE permitido).
		if _, err := tx.Exec(ctx, `
			UPDATE catalogos.valor_catalogo SET vigente_hasta = $1
			 WHERE id = $2 AND version = $3`, ahora, id, versionActual); err != nil {
			return fmt.Errorf("cerrar vigencia: %w", err)
		}

		// Insertar la nueva versión, vigente desde ahora.
		rows, err := tx.Query(ctx, `
			INSERT INTO catalogos.valor_catalogo
			  (id, version, tipo, codigo, etiqueta, estado, vigente_desde)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+columnas,
			id, nuevaVersion, string(tipo), codigo, etiqueta, estado, ahora)
		if err != nil {
			return fmt.Errorf("insertar nueva versión: %w", err)
		}
		defer rows.Close()
		items, err := escanearValores(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New("insert sin RETURNING")
		}
		nuevo = items[0]

		return internal.PublicarActualizacion(ctx, r.pub, tx, internal.EntidadActualizada{
			Entidad: entidadEvento, EntidadID: id.String(), Version: nuevaVersion, SubTipo: string(tipo),
		})
	})
	if err != nil {
		if errors.Is(err, ErrNoEncontrado) {
			return catclient.ValorCatalogo{}, err
		}
		return catclient.ValorCatalogo{}, fmt.Errorf("tipos: actualizar: %w", err)
	}
	return nuevo, nil
}

// escanearValores mapea las filas (con columnas = `columnas`) a []ValorCatalogo.
// El schema de lectura no expone vigencias; solo id, version, codigo, etiqueta y
// estado.
func escanearValores(rows pgx.Rows) ([]catclient.ValorCatalogo, error) {
	var out []catclient.ValorCatalogo
	for rows.Next() {
		var (
			id       catclient.Id
			version  int
			codigo   string
			etiqueta string
			estado   string
		)
		if err := rows.Scan(&id, &version, &codigo, &etiqueta, &estado); err != nil {
			return nil, fmt.Errorf("tipos: escanear: %w", err)
		}
		v := version
		out = append(out, catclient.ValorCatalogo{
			Id:       id,
			Version:  &v,
			Codigo:   codigo,
			Etiqueta: etiqueta,
			Estado:   catclient.ValorCatalogoEstado(estado),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tipos: filas: %w", err)
	}
	return out, nil
}
