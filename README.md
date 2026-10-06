# pf-svc-catalogos

Módulo `catalogos` de AI27: el registro maestro de las entidades sobre las que
la operación trabaja —clientes, contratos, SLA, vehículos de AI27 (coche/moto),
custodios y catálogos de tipo—. Es una **hoja del grafo** (`catalogo -> []`):
no llama a `operacion` ni a ningún módulo de dominio. Vive dentro del deployable
`core`.

Estado: **scaffolding inicial** (prompt `02`). El cableado y la estructura
existen; la lógica de negocio real todavía no.

## Frontera (ADR 31)

`catalogos` es dueño de los **átomos** (vehículo, custodio). **No** es dueño de
la `unidad` (paquete vehículo + custodios), su roster, disponibilidad ni
reserva, que son de `operacion`. La costura: `catalogos` responde *qué es y si
es apto*; `operacion` responde *si está libre y la toma*. No existe aquí una
bandera `disponible`, y en V1 la **aptitud es informativa**: no gatea armar ni
asignar (DEC-055). `patrulla`/`moto` son tipos de unidad derivados del tipo de
vehículo, nunca columna ni recurso aparte.

## Superficie pública

`catalogos.go` expone `New(Pools, Deps)`, `Routes`, `Workers` y `Consultas`
(00 "Forma de un módulo"). `catalogos` es destino de aristas síncronas
(`operacion`, `reportes`, `bff` en proceso; `mensajeria` siempre por HTTP), por
eso expone `Consultas()`, tipada solo con tipos de `pf-pkg-contracts`.

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

## Brechas de contrato que bloquean partes del scaffold

Verificado contra `pf-pkg-contracts` en `v0.1.0-tracer.3` (contrato pinned). Lo
presente se implementa; lo ausente se reporta y **no se escribe a mano**
(contrato primero, 00 regla 6).

| # | Superficie | Estado en tracer.3 | Efecto |
|---|---|---|---|
| C1 | Cliente Go `api-client-go/catalogos` | ✅ presente | — |
| C2 | Lectura de clientes, contrato, SLA | ✅ presente | implementable |
| C11 | Aptitud de custodio y "unidad" | ✅ presente (en `unidades`) | implementable como lectura informativa |
| C3 | Átomo **vehículo** (ADR 31) en lugar de `Unidad` (ADR 24) | ❌ falta (contrato aún en `/unidades`, modelo viejo) | **bloquea** el paquete `vehiculos`, su tabla y rutas; la aptitud se expone sobre `unidades` mientras tanto |
| C4 | Lectura por lote de vehículos/custodios | ❌ falta | bloquea lecturas `:lote` |
| C5 | Lectura por lote de clientes/contratos/SLA | ❌ falta | bloquea lecturas `:lote` de `bff`/`reportes`/`mensajeria` |
| C6 | **Versión** en toda respuesta de lectura | ❌ falta en los schemas | las lecturas no exponen versión todavía |
| C7 | Superficie admin `POST`/`PATCH` por agregado | ❌ falta | bloquea toda escritura administrativa (E5) |
| C8 | Catálogos de tipo (tipos, líneas, GPS, zonas lista) | ❌ falta | bloquea el paquete `tipos`, sus tablas y rutas |
| C9 | Lectura a una fecha de vehículos/custodios | ❌ falta | — |
| C10 | `catalogo.actualizado` con **versión** | ❌ falta (proto sin campo versión) | la versión viaja en el sobre de dominio; el payload del evento se alineará cuando el contrato la agregue |

C3–C10 son la **ola 1 contractual** (`tracer.4`), bloqueada además por la
reconciliación pendiente de `catalogos.yaml` con ADR 31 (ver nota abajo).

## Diferido a V2 (no se crea)

`ruta` y geometría de zonas de riesgo (ADR 20, DEC-017); `parametro_operativo`
(umbral de `SIN_SEÑAL` y de estadía prolongada; forma bloqueada por ADR 5,
DEC-038); kit de seguridad y tipos de custodia (DEC-047). Los paquetes `rutas` y
`parametros` solo declaran el diferimiento.

## Contradicciones reportadas (no resueltas aquí)

- **Consumidores de `catalogo.actualizado`:** `catalogos.md` (g) lista cinco,
  E5-T5 cuatro, y `contracts/events/INVENTARIO.md` solo `operacion`. No afecta al
  productor; debe reconciliarse antes de que `bff`/`reportes`/`mensajeria` se
  suscriban.
- **Documentos aún en el modelo viejo** (`catalogos.md` (d)/(e), E5-T1/T3,
  `operacion.md` (f), comentario de `catalogo.proto`): siguen diciendo "unidad"
  donde ADR 31 dice vehículo. No se editan desde este scaffold.
- **Nombre canónico del átomo vehículo:** `vehiculos` es nombre de trabajo; se
  fija en `pf-pkg-contracts` (C3) y en `naming-conventions.md`.
- **LFPDPPP** (ARCO vs retención probatoria de P8 sobre `custodio`): pendiente,
  anotado en el paquete `custodios`.

## Ramas

`pf-cat-m` / `pf-cat-d` / `pf-cat-q`. Se trabaja en `pf-cat-d`.
