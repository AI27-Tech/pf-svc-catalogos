// Package tipos gestiona los catálogos de tipo (listas de valores) de V1:
// tipos de servicio/monitoreo, líneas de transporte, tipos de unidad (incluye
// `Patrulla`, la unidad en su rol de escolta — ADR 31 / DEC-044), tipos de
// custodia y zonas de riesgo como LISTA SIMPLE (sin geometría)
// (E5-T3, E5-HU-023, DEC-017 / DEC-047). Sub-módulo interno de catalogos.
//
// Las cinco listas comparten forma (código, etiqueta, estado, versión) y viven
// en UNA sola tabla `valor_catalogo` parametrizada por `tipo` (opción A del
// contrato tracer.8). Desactivar un valor no rompe registros históricos
// (E5-HU-023): los valores se versionan por vigencia (P8), no se borran;
// desactivar es crear una nueva versión con estado `inactivo`, y la versión que
// regía antes sigue siendo legible "a una fecha" (C9).
//
// Implementado sobre el contrato v0.1.0-tracer.8, que cerró la brecha C8
// (recurso /api/v1/catalogos-tipo/{tipo}): lectura por lote (listarValoresCatalogo)
// y escritura administrativa (crearValorCatalogo / actualizarValorCatalogo), con
// el mismo patrón versión-en-outbox (`catalogo.actualizado`, P17) que vehículos
// y custodios.
//
// AUTORIZACIÓN POR ROL — PENDIENTE (DEC-047 #1): sólo los Coordinadores crean
// líneas de transporte. En V1 NO se implementa aquí (decisión A1 de la sesión):
// catalogos todavía no tiene mecanismo de autorización por rol de negocio —la
// única autorización presente es de infraestructura (revocación de sesión y
// grants de Postgres por tabla)— y el contrato modela las cinco listas bajo un
// solo recurso sin gancho por tipo. La escritura no distingue rol; la regla
// queda anotada para cuando se resuelva la autorización por rol en general.
package tipos
