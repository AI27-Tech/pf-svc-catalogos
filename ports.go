package catalogos

import (
	platformerrors "github.com/AI27-Tech/pf-pkg-platform-go/errors"
)

// catalogos es HOJA del grafo (catalogo -> []): no declara puertos de salida de
// dominio. La única arista saliente es la restringida a identidad para
// revocación de sesión (RevocacionPort, en catalogos.go, cableada por la raíz
// vía Deps), que no es una dependencia de dominio sino de infraestructura de
// autorización (DEC-051/058). arch-test verifica esta ausencia de aristas
// contra pf-pkg-platform-go/tools/arch-test/reglas.yaml.

// errNoImplementado marca un caso de uso aún no implementado en el scaffold.
// Usa la taxonomía de la plataforma (I3) para que la respuesta sea coherente
// entre el adaptador HTTP y el en proceso.
func errNoImplementado(op string) error {
	return platformerrors.NewRoto("catalogos: %s no implementado todavía (scaffold)", op)
}
