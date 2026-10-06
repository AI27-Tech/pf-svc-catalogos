// Package custodios gestiona el ÁTOMO custodio (persona): identidad, aptitud
// informativa, estado, y una referencia POR IDENTIFICADOR (no FK) al usuario de
// identidad si existe (ADR 31 §5, E5-HU-022). Sub-módulo interno de catalogos.
//
// El custodio NO se clasifica: no hay "tipo de custodia" (E5-HU-022/023,
// DEC-044 notas). La aptitud está separada de la lectura simple y es
// INFORMATIVA en V1 (DEC-055): nunca gatea.
//
// LFPDPPP: el derecho ARCO de cancelación del titular convive con la retención
// probatoria de P8 sobre custodio (catalogos.md (e)); la resolución queda
// pendiente y no se decide en el scaffold.
//
// Scaffolding: lectura y aptitud ya en el contrato (C2/C11); lectura por lote
// (C4) y a una fecha (C9) bloqueadas por brecha de contrato.
package custodios
