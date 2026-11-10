// Package clientes gestiona el expediente comercial del cliente y sus contratos,
// versionados por vigencia (catalogos.md (d)/(e), E5-HU-024). Es un sub-módulo
// interno de catalogos: expone su superficie a sus hermanos por este paquete y
// oculta la implementación en subpaquetes internal/ anidados.
//
// Lectura y escritura de contratos, y lectura del cliente, desde tracer.8. El
// alta (idempotente por id) y la edición del expediente del cliente llegan con
// tracer.40 (E8-T19, DEC-144 #2).
package clientes
