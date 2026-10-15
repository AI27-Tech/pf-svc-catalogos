package tipos

import (
	"testing"

	catclient "github.com/AI27-Tech/pf-pkg-contracts/packages/api-client-go/catalogos"
)

// TestValidarTipo_AceptaLosCinco confirma que los cinco catálogos de tipo de V1
// (enum TipoCatalogo de tracer.8) se aceptan.
func TestValidarTipo_AceptaLosCinco(t *testing.T) {
	validos := []catclient.TipoCatalogo{
		catclient.TiposServicio,
		catclient.LineasTransporte,
		catclient.TiposUnidad,
		catclient.TiposCustodia,
		catclient.ZonasRiesgo,
	}
	for _, tipo := range validos {
		if err := ValidarTipo(tipo); err != nil {
			t.Errorf("ValidarTipo(%q) debía aceptar, dio: %v", tipo, err)
		}
	}
}

// TestValidarTipo_RechazaDesconocido confirma que un tipo fuera del enum se
// rechaza con ErrTipoInvalido (lo que el handler traduce a 400).
func TestValidarTipo_RechazaDesconocido(t *testing.T) {
	casos := []catclient.TipoCatalogo{"", "rutas", "parametros", "tipos_servicio ", "TIPOS_SERVICIO"}
	for _, tipo := range casos {
		if err := ValidarTipo(tipo); err == nil {
			t.Errorf("ValidarTipo(%q) debía rechazar, no dio error", tipo)
		}
	}
}
