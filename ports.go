package catalogos

// catalogos es HOJA del grafo (catalogo -> []): no declara puertos de salida de
// dominio. La única arista saliente es la restringida a identidad para
// revocación de sesión (RevocacionPort, en catalogos.go, cableada por la raíz
// vía Deps), que no es una dependencia de dominio sino de infraestructura de
// autorización (DEC-051/058). arch-test verifica esta ausencia de aristas
// contra pf-pkg-platform-go/tools/arch-test/reglas.yaml.
