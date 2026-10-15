# pf-svc-catalogos

Módulo `catalogos` de AI27: el registro maestro de las entidades sobre las que
la operación trabaja —clientes, contratos, SLA, vehículos de AI27 (coche/moto),
custodios y catálogos de tipo—. Es una **hoja del grafo** (`catalogo -> []`):
no llama a `operacion` ni a ningún módulo de dominio. Vive dentro del deployable
`core`.

Estado: los sub-módulos `vehiculos` (E5-T1), `custodios` (E5-T2) y `tipos`
(E5-T3, catálogos de tipo) están **implementados** sobre el contrato
`v0.1.0-tracer.8` (lectura por lote, lectura a una fecha donde aplica, aptitud
informativa, y escritura `POST`/`PATCH` con outbox en la misma transacción).
El resto de los sub-módulos (`clientes`, `sla`) siguen en scaffolding.

## Frontera (ADR 31)

`catalogos` es dueño de los **átomos** (vehículo, custodio). **No** es dueño de
la `unidad` (paquete vehículo + custodios), su roster, disponibilidad ni
reserva, que son de `operacion`. La costura: `catalogos` responde *qué es y si
es apto*; `operacion` responde *si está libre y la toma*. No existe aquí una
bandera `disponible`, y en V1 la **aptitud es informativa**: no gatea armar ni
asignar (DEC-055). `patrulla`/`moto` son tipos de unidad derivados del tipo de
vehículo, nunca columna ni recurso aparte.

## Superficie pública

`catalogos.go` expone `New(Pools, Deps)`, `Routes`, `Workers`, `Consultas` y
`Comandos` (00 "Forma de un módulo"). `catalogos` es destino de aristas
síncronas (`operacion`, `reportes`, `bff` en proceso; `mensajeria` siempre por
HTTP), por eso expone `Consultas()` (lectura) y `Comandos()` (escritura
administrativa), ambas tipadas solo con tipos de `pf-pkg-contracts`.

Verifica tokens y consume `sesion.revocada` por su propia suscripción durable
(DEC-057/058), con la arista restringida a `identidad` (`listarRevocadas` +
`obtenerSesion`, DEC-051).

## Correr standalone (I5)

```bash
SERVICE_NAME=catalogos HTTP_ADDR=:8080 \
  DATABASE_URL="postgres://svc_catalogos@localhost/ai27?sslmode=disable" \
  go run ./cmd/api
curl localhost:8080/health   # {"status":"ok"}
curl localhost:8080/ready    # {"status":"ok"} | 503
```

Un solo esquema `catalogos`, un solo rol `svc_catalogos`, un solo pool
(00 regla 4). Toda entidad se versiona por vigencia; sin `UPDATE` que pise
historia ni `DELETE` en firme (P8).

## Estado de las superficies de contrato

Verificado contra `pf-pkg-contracts` en `v0.1.0-tracer.8` (contrato pinned). Lo
presente se implementa; lo ausente se reporta y **no se escribe a mano**
(contrato primero, 00 regla 6). Las superficies de vehículos (E5-T1), custodios
(E5-T2) y catálogos de tipo (E5-T3) ya están cerradas e implementadas; quedan
abiertas C5 y C10 (otras historias).

| # | Superficie | Estado en tracer.8 | Efecto |
|---|---|---|---|
| C1 | Cliente Go `api-client-go/catalogos` | ✅ presente | — |
| C2 | Lectura de clientes, contrato, SLA | ✅ presente | implementable |
| C11 | Aptitud de custodio y vehículo | ✅ presente | lectura informativa (DEC-055) |
| C3 | Átomo **vehículo** (ADR 31) en lugar de `Unidad` (ADR 24) | ✅ cerrada en tracer.4 | `/api/v1/vehiculos`, schema `Vehiculo`; paquete `vehiculos` implementado (E5-T1) |
| C4 | Lectura por lote de vehículos **y custodios** | ✅ vehículos en tracer.5, custodios en tracer.8 | `GET /vehiculos?ids=...` y `GET /custodios?ids=...` implementados |
| C6 | **Versión** en la respuesta de lectura | ✅ vehículos en tracer.5, custodios en tracer.8 | `Vehiculo.version` y `Custodio.version` (opcionales) implementados |
| C7 | Superficie admin `POST`/`PATCH` de vehículos **y custodios** | ✅ vehículos en tracer.5, custodios en tracer.8 | `crear/actualizarVehiculo` y `crear/actualizarCustodio` con outbox (P17) implementados |
| C9 | Lectura a una fecha de vehículos **y custodios** | ✅ vehículos en tracer.5, custodios en tracer.8 | `GET /{vehiculos,custodios}/{id}?a_fecha=...` implementado |
| C8 | Catálogos de tipo (tipos, líneas, unidad, custodia, zonas lista) | ✅ cerrada en tracer.8 | `/api/v1/catalogos-tipo/{tipo}`, schema `ValorCatalogo`; paquete `tipos` implementado (E5-T3) |
| C5 | Lectura por lote de clientes/contratos/SLA | ❌ falta | bloquea lecturas `:lote` de `bff`/`reportes`/`mensajeria` |
| C10 | `catalogo.actualizado` con **versión** | ❌ falta (proto sin campo versión) | la versión viaja en el sobre de dominio; el payload del evento se alineará cuando el contrato la agregue |

C3/C4/C6/C7/C9 para vehículos (E5-T1) se cerraron en `tracer.5`; las mismas
superficies para custodios (E5-T2) y la superficie de catálogos de tipo C8
(E5-T3) en `tracer.8`, y ya están implementadas. C5 (lote de otros agregados) y
C10 (versión en el evento) siguen abiertas: son otras historias.

### Pendientes de modelado reportados en E5-T2 (no resueltos aquí)

- **Aptitud de custodio por cliente:** el contrato declara `cliente_id`
  obligatorio en `GET /custodios/{id}/aptitud`, por simetría con vehículo, pero
  el custodio **no pertenece a un cliente** (su tabla no tiene `cliente_id`). La
  implementación acepta el parámetro por conformidad y **no acota** por él; la
  aptitud se evalúa solo por `estado` (DEC-055, informativa). De dónde sale la
  relación custodio↔cliente —si la hay en V1— es decisión de Operación/Lead.
- **Documentos del custodio:** a diferencia de vehículo, el custodio no tiene
  tabla de documentos (E5-HU-022 los dejó "Por confirmar"), así que la aptitud
  no evalúa vencimientos. Pendiente de Operación.

## Catálogos de tipo (E5-T3) — pendiente de autorización por rol

Las cinco listas de V1 (`tipos_servicio`, `lineas_transporte`, `tipos_unidad`
—incluye `Patrulla`—, `tipos_custodia`, `zonas_riesgo` como lista sin geometría)
están implementadas en el paquete `tipos` bajo una sola tabla `valor_catalogo`
parametrizada por `tipo`. Lectura por lote y escritura `POST`/`PATCH`;
desactivar es un `PATCH` a `estado: inactivo` que crea una nueva versión sin
romper la historia (P8).

**Pendiente (DEC-047 #1):** sólo los Coordinadores crean líneas de transporte.
En V1 **no** se implementa la autorización por rol (no hay aún mecanismo de rol
de negocio en `catalogos` y el contrato no da gancho por tipo); la escritura no
distingue rol. Queda anotado en `internal/tipos/doc.go` para cuando se resuelva
la autorización por rol en general.

## Diferido a V2 (no se crea)

`ruta` y **geometría** de zonas de riesgo —en V1 entran solo como lista de
valores— (ADR 20, DEC-017); `parametro_operativo` (umbral de `SIN_SEÑAL` y de
estadía prolongada; forma bloqueada por ADR 5, DEC-038); kit de seguridad,
retirado de V1 (DEC-047). Los paquetes `rutas` y `parametros` solo declaran el
diferimiento.

## Contradicciones reportadas (no resueltas aquí)

- **Consumidores de `catalogo.actualizado`:** `catalogos.md` (g) lista cinco,
  E5-T5 cuatro, y `contracts/events/INVENTARIO.md` solo `operacion`. No afecta al
  productor; debe reconciliarse antes de que `bff`/`reportes`/`mensajeria` se
  suscriban.
- **Documentos aún en el modelo viejo** (`catalogos.md` (d)/(e), E5-T1/T3,
  `operacion.md` (f), comentario de `catalogo.proto`): siguen diciendo "unidad"
  donde ADR 31 dice vehículo. No se editan desde este scaffold.
- **Nombre canónico del átomo vehículo:** fijado como `vehiculo` en
  `pf-pkg-contracts` (C3, tracer.4) y en `naming-conventions.md`. Ya no es nombre
  de trabajo.
- **LFPDPPP** (ARCO vs retención probatoria de P8 sobre `custodio`): pendiente,
  anotado en el paquete `custodios`.

## Ramas

`pf-cat-m` / `pf-cat-d` / `pf-cat-q`. Se trabaja en `pf-cat-d`.
