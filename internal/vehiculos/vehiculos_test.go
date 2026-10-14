package vehiculos

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// rowFalso devuelve valores predefinidos en Scan, en el orden en que Aptitud
// hace sus QueryRow: (1) el estado del vehículo, (2) el count de documentos
// vencidos.
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

// consultasFalso implementa la interfaz Consultas devolviendo rows de una cola.
// Solo se usa QueryRow en Aptitud; Query entra en pánico si se llama.
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

// Begin satisface la interfaz Comandos. Las pruebas de aptitud no escriben, así
// que no se usa; la escritura transaccional (Crear/Actualizar) se prueba en
// integración contra Postgres (grants_integration_test.go cubre los permisos y
// las pruebas de BD real el versionado + outbox).
func (c *consultasFalso) Begin(ctx context.Context) (pgx.Tx, error) {
	panic("Begin no usado en estas pruebas de aptitud")
}

func nuevoRepo(estado string, vencidos int) *Repositorio {
	return NewRepositorio(&consultasFalso{rows: []pgx.Row{
		rowFalso{valores: []any{estado}},
		rowFalso{valores: []any{vencidos}},
	}})
}

// TestAptitud_ActivoYDocumentosVigentes: estado activa + 0 vencidos ⇒ apto.
func TestAptitud_ActivoYDocumentosVigentes(t *testing.T) {
	repo := nuevoRepo("activa", 0)
	ap, err := repo.Aptitud(context.Background(), uuid.New(), uuid.New(), time.Now())
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

// TestAptitud_DocumentoVencido es el criterio de aceptación clave de E5-T1:
// una unidad con documento vencido responde "no apta" (DEC-055: informativa).
func TestAptitud_DocumentoVencido(t *testing.T) {
	repo := nuevoRepo("activa", 1)
	ap, err := repo.Aptitud(context.Background(), uuid.New(), uuid.New(), time.Now())
	if err != nil {
		t.Fatalf("Aptitud: %v", err)
	}
	if ap.Apto {
		t.Fatal("se esperaba apto=false por documento vencido")
	}
	if !contiene(ap.Motivos, "documento vencido") {
		t.Errorf("se esperaba el motivo 'documento vencido', motivos: %v", deref(ap.Motivos))
	}
}

// TestAptitud_VehiculoInactivo: estado inactiva ⇒ no apto con su motivo.
func TestAptitud_VehiculoInactivo(t *testing.T) {
	repo := nuevoRepo("inactiva", 0)
	ap, err := repo.Aptitud(context.Background(), uuid.New(), uuid.New(), time.Now())
	if err != nil {
		t.Fatalf("Aptitud: %v", err)
	}
	if ap.Apto {
		t.Fatal("se esperaba apto=false por vehículo inactivo")
	}
	if !contiene(ap.Motivos, "vehículo inactivo") {
		t.Errorf("se esperaba el motivo 'vehículo inactivo', motivos: %v", deref(ap.Motivos))
	}
}

// TestAptitud_NoEncontrado: sin fila vigente ⇒ ErrNoEncontrado.
func TestAptitud_NoEncontrado(t *testing.T) {
	repo := NewRepositorio(&consultasFalso{rows: []pgx.Row{
		rowFalso{err: pgx.ErrNoRows},
	}})
	_, err := repo.Aptitud(context.Background(), uuid.New(), uuid.New(), time.Now())
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
