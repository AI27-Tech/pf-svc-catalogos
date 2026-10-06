// Package vehiculos gestiona el ÁTOMO vehículo de AI27 (coche/moto) con su tipo,
// identidad (económico, placas), aptitud informativa y estado (ADR 31 §5,
// E5-HU-021). Sub-módulo interno de catalogos.
//
// `patrulla` y `moto` son TIPOS DE UNIDAD derivados del tipo de vehículo
// (coche → patrulla, moto → moto); nunca una columna ni un recurso aparte
// (ADR 31, invariantes). La `unidad` (paquete vehículo + custodios), su roster,
// disponibilidad y reserva son de operacion, NO de aquí.
//
// NOMBRE DE TRABAJO: "vehiculos" es provisional. El nombre canónico del átomo
// (paquete, tabla, ruta) se fija en pf-pkg-contracts (brecha C3) y en
// naming-conventions.md; no se fija por inferencia aquí.
//
// BLOQUEADO (brecha C3): el contrato tracer.3 todavía modela `/api/v1/unidades`
// con el modelo viejo de ADR 24 ("vehículos de flota"), no el átomo vehículo de
// ADR 31. Hasta que el contrato se reconcilie (ola 1, tracer.4) no hay tabla
// `vehiculo` ni rutas propias: este paquete queda sin implementación. La aptitud
// presente en el contrato (aptitudUnidad) se expone en Consultas() como lectura
// informativa (DEC-055), a la espera de pasar a vehículo con C3.
package vehiculos
