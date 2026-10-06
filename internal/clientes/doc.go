// Package clientes gestiona el expediente comercial del cliente y sus contratos,
// versionados por vigencia (catalogos.md (d)/(e), E5-HU-024). Es un sub-módulo
// interno de catalogos: expone su superficie a sus hermanos por este paquete y
// oculta la implementación en subpaquetes internal/ anidados.
//
// Scaffolding: la lectura de cliente, contrato y SLA ya está en el contrato
// (C2), así que estos casos de uso son los primeros a implementar. La
// administración (POST/PATCH) está bloqueada por la brecha C7 (el contrato no
// tiene superficie admin) y las lecturas por lote por C5.
package clientes
