package internal

import (
	"context"
	"strings"
	"testing"

	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// txFalso captura las sentencias Exec para verificar que catalogo.actualizado se
// escribe en la outbox dentro de la transacción. Solo implementa lo que el
// Publisher usa (Exec); el resto de pgx.Tx entra en pánico si se llama.
type txFalso struct {
	execs []execCapturada
}

type execCapturada struct {
	sql  string
	args []any
}

func (t *txFalso) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, execCapturada{sql: sql, args: args})
	return pgconn.CommandTag{}, nil
}

func (t *txFalso) Begin(ctx context.Context) (pgx.Tx, error) { panic("no usado") }
func (t *txFalso) Commit(ctx context.Context) error          { return nil }
func (t *txFalso) Rollback(ctx context.Context) error        { return nil }
func (t *txFalso) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("no usado")
}
func (t *txFalso) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { panic("no usado") }
func (t *txFalso) LargeObjects() pgx.LargeObjects                         { panic("no usado") }
func (t *txFalso) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("no usado")
}
func (t *txFalso) Query(context.Context, string, ...any) (pgx.Rows, error) { panic("no usado") }
func (t *txFalso) QueryRow(context.Context, string, ...any) pgx.Row        { panic("no usado") }
func (t *txFalso) Conn() *pgx.Conn                                         { return nil }

// TestPublicarActualizacion_EscribeEnLaOutbox verifica que el helper inserta
// catalogo.actualizado en la outbox dentro de la tx dada, con el tipo correcto y
// la identidad de la entidad como clave de orden (DoD: "al menos un caso de uso
// escribe una nueva versión y su catalogo.actualizado en la misma transacción").
func TestPublicarActualizacion_EscribeEnLaOutbox(t *testing.T) {
	tx := &txFalso{}
	pub := outbox.NewPublisher()

	err := PublicarActualizacion(context.Background(), pub, tx, EntidadActualizada{
		Entidad:   "cliente",
		EntidadID: "11111111-1111-1111-1111-111111111111",
		Version:   2,
	})
	if err != nil {
		t.Fatalf("PublicarActualizacion: %v", err)
	}

	if len(tx.execs) != 1 {
		t.Fatalf("se esperaba 1 INSERT en la outbox, hubo %d", len(tx.execs))
	}
	ex := tx.execs[0]
	if !strings.Contains(strings.ToUpper(ex.sql), "INSERT INTO OUTBOX") {
		t.Errorf("la sentencia no inserta en outbox: %q", ex.sql)
	}
	// args: evento_id, tipo, version, agregado_id, sensibilidad, ocurrido_en, payload, trace_context
	if ex.args[1] != TipoCatalogoActualizado {
		t.Errorf("tipo = %v, se esperaba %q", ex.args[1], TipoCatalogoActualizado)
	}
	if ex.args[2] != 2 {
		t.Errorf("version = %v, se esperaba 2", ex.args[2])
	}
	if ex.args[3] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("agregado_id = %v, se esperaba la identidad de la entidad", ex.args[3])
	}
}

// TestPublicarActualizacion_RechazaEntidadVacia confirma la validación mínima.
func TestPublicarActualizacion_RechazaEntidadVacia(t *testing.T) {
	tx := &txFalso{}
	pub := outbox.NewPublisher()
	if err := PublicarActualizacion(context.Background(), pub, tx, EntidadActualizada{Version: 1}); err == nil {
		t.Fatal("entidad/entidad_id vacíos deberían dar error")
	}
}

var _ pgx.Tx = (*txFalso)(nil)
