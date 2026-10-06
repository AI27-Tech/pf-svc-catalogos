// Package tipos gestiona los catálogos de tipo (listas de valores) de V1:
// tipos de servicio/monitoreo, tipos de unidad (patrulla/moto), tipos de tracto,
// líneas de transporte por cliente, lista de GPS (inventario de dispositivos,
// SIN posición) y zonas de riesgo como LISTA SIMPLE (sin geometría)
// (E5-T3, E5-HU-023, DEC-047). Sub-módulo interno de catalogos.
//
// Desactivar un valor de tipo no rompe registros históricos (E5-HU-023): los
// valores se versionan por vigencia, no se borran.
//
// BLOQUEADO (brecha C8): el contrato tracer.3 no tiene la superficie de
// catálogos de tipo (lectura ni administración). Sin tablas ni rutas hasta C8.
//
// TODO(catalogos): solo los Coordinadores crean líneas de transporte
// (DEC-047 #1), lo que exige autorización por rol en la superficie admin;
// pendiente hasta C8.
package tipos
