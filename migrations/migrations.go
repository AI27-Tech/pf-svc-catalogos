// Package migrations expone las migraciones SQL del esquema catalogos como un
// embed.FS. La raíz de composición (core) y el arranque standalone (cmd/api)
// las aplican con el rol dueño del esquema (svc_catalogos). platform-go no
// administra schemas: cada módulo trae los suyos (00 "Forma de un módulo").
package migrations

import "embed"

// FS contiene los archivos .sql de migración, ordenados por nombre.
//
//go:embed catalogos/*.sql
var FS embed.FS
