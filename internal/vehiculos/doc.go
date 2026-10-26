// Package vehiculos gestiona el ÁTOMO vehículo de AI27 (coche/moto) con su tipo,
// identidad (económico, placas), aptitud informativa y estado (ADR 31 §5,
// E5-HU-021). Sub-módulo interno de catalogos.
//
// `patrulla` y `moto` son TIPOS DE UNIDAD derivados del tipo de vehículo
// (coche → patrulla, moto → moto); nunca una columna ni un recurso aparte
// (ADR 31, invariantes). La `unidad` (paquete vehículo + custodios), su roster,
// disponibilidad y reserva son de operacion, NO de aquí.
//
// Sobre el contrato v0.1.0-tracer.6 (brecha C3 cerrada: el átomo vive en
// /api/v1/vehiculos). Implementa la superficie completa de E5-T1:
//   - lectura paginada por cursor y lectura por lote (C4);
//   - lectura vigente o "a una fecha" (C9), con `version` en la respuesta (C6);
//   - aptitud informativa (DEC-055), evaluada contra el contrato/SLA de un
//     cliente (de ahí clienteID);
//   - escritura administrativa `crearVehiculo`/`actualizarVehiculo` (C7), que
//     versiona por vigencia (nunca sobrescribe, P8) y emite
//     `catalogo.actualizado` en la misma transacción (P17).
package vehiculos
