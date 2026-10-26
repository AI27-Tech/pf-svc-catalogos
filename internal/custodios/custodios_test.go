package custodios

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// rowFalso devuelve valores predefinidos en Scan. Para la aptitud del custodio
// solo hay UN QueryRow: el estado de la versión vigente (a diferencia de
// vehiculo, que además cuenta documentos vencidos — custodio no tiene tabla de
// documentos en V1, E5-HU-022).
type rowFalso struct {
	valores []any
	err     error
}

func (r rowFalso) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = r.valores[i].(string)
		case *int:
			*d = r.valores[i].(int)
		default:
			// destino no soportado por el fake
		}
	}
	return nil
}

// consultasFalso implementa Consultas devolviendo rows de una cola. Solo se usa
// QueryRow en Aptitud; Query/Begin entran en pánico si se llaman.
type consultasFalso struct {
	rows []pgx.Row
	i    int
}

func (c *consultasFalso) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	r := c.rows[c.i]
	c.i++
	return r
}

func (c *consultasFalso) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	panic("Query no usado en estas pruebas de aptitud")
}

func (c *consultasFalso) Begin(ctx context.Context) (pgx.Tx, error) {
	panic("Begin no usado en estas pruebas de aptitud")
}

func nuevoRepo(estado string) *Repositorio {
	return NewRepositorio(&consultasFalso{rows: []pgx.Row{
		rowFalso{valores: []any{estado}},
	}})
}

// TestAptitud_Activo: estado activo ⇒ apto, sin motivos.
func TestAptitud_Activo(t *testing.T) {
	repo := nuevoRepo("activo")
	ap, err := repo.Aptitud(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Aptitud: %v", err)
	}
	if !ap.Apto {
		t.Errorf("se esperaba apto=true, fue false (motivos: %v)", deref(ap.Motivos))
	}
	if ap.Motivos != nil {
		t.Errorf("se esperaban sin motivos, hubo: %v", *ap.Motivos)
	}
}

// TestAptitud_Inactivo: estado inactivo ⇒ no apto con su motivo (DEC-055:
// informativa, nunca bloquea).
func TestAptitud_Inactivo(t *testing.T) {
	repo := nuevoRepo("inactivo")
	ap, err := repo.Aptitud(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Aptitud: %v", err)
	}
	if ap.Apto {
		t.Fatal("se esperaba apto=false por custodio inactivo")
	}
	if !contiene(ap.Motivos, "custodio inactivo") {
		t.Errorf("se esperaba el motivo 'custodio inactivo', motivos: %v", deref(ap.Motivos))
	}
}

// TestAptitud_NoEncontrado: sin fila vigente ⇒ ErrNoEncontrado.
func TestAptitud_NoEncontrado(t *testing.T) {
	repo := NewRepositorio(&consultasFalso{rows: []pgx.Row{
		rowFalso{err: pgx.ErrNoRows},
	}})
	_, err := repo.Aptitud(context.Background(), uuid.New())
	if err != ErrNoEncontrado {
		t.Fatalf("se esperaba ErrNoEncontrado, fue: %v", err)
	}
}

func contiene(ss *[]string, s string) bool {
	if ss == nil {
		return false
	}
	for _, v := range *ss {
		if v == s {
			return true
		}
	}
	return false
}

func deref(ss *[]string) []string {
	if ss == nil {
		return nil
	}
	return *ss
}
