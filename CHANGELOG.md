# CHANGELOG

## 2026:10:15 (E5-T2 — superficie de custodios sobre tracer.8)

**Resumen:** Se implementa la superficie completa del sub-módulo `custodios` (E5-T2 / AI27-74) sobre el contrato `v0.1.0-tracer.8`, espejando la vertical de vehículos de E5-T1. Lectura paginada y por lote, lectura vigente o a una fecha con `version`, aptitud informativa, y escritura administrativa `POST`/`PATCH` que versiona por vigencia (no sobrescribe, P8) y emite `catalogo.actualizado` en la misma transacción (P17). El custodio referencia al usuario de `identidad` por identificador (`usuario_id`), nunca por FK entre schemas (P1).

**Cambios:**
- `go.mod`: pin de `api-client-go` subido de `v0.1.0-tracer.6` a `v0.1.0-tracer.8`. El cliente publicado de tracer.6 no traía la superficie de escritura de custodios (sin `CustodioEntrada`/`CustodioCambios` ni `version`/`usuario_id`/vigencias en `Custodio`); tracer.8 la publica. Sin `replace` local.
- `migrations/catalogos/0005_custodio_cols.sql` (nuevo): `ALTER TABLE catalogos.custodio ADD COLUMN telefono TEXT`. La tabla `custodio` ya existía desde `0001` (versionada por vigencia) y sus grants desde `0002`; esta migración solo agrega el atributo de contacto que el contrato expone. No requiere fase de grants nueva: `0002` otorga SELECT/INSERT (nivel tabla) y UPDATE solo de `vigente_hasta` sobre `custodio`.
- `internal/custodios/custodios.go` (nuevo): repositorio con `Listar` (cursor), `ListarPorLote` (C4), `Obtener` (vigente o a una fecha, C9), `Aptitud` (informativa por `estado`, DEC-055), `Crear` y `Actualizar` (C7) con `pgx.BeginFunc` + outbox en la misma tx (P17, `Entidad: "custodio"`). `escanearCustodios` mapea `version` y vigencia (C6); helper `textoUUID` para el `usuario_id` TEXT↔UUID. A diferencia de vehículo: sin `cliente_id`, enums `activo`/`inactivo`, y la aptitud no evalúa documentos (no existe tabla).
- `catalogos.go`: `ConsultasAPI` ampliada (`ListarCustodios`, `ListarCustodiosPorLote`, `ObtenerCustodio`); `ComandosAPI` ampliada (`CrearCustodio`, `ActualizarCustodio`); `AptitudCustodio` deja de ser stub. `Routes()` monta `GET /custodios` (con `?ids` para lote, excluyente con `cursor`), `POST /custodios`, `GET /custodios/{id}` (con `?a_fecha`), `PATCH /custodios/{id}` y la aptitud. Reutiliza los helpers de vehículos.
- `internal/custodios/custodios_test.go` (nuevo): 3 pruebas unitarias de aptitud (fake de `pgx.Row`; la aptitud del custodio hace un solo `QueryRow`).
- `internal/custodios/custodios_integracion_test.go` (nuevo): integración contra Postgres — `TestCrear_EmiteEventoEnLaMismaTx` (P17), `TestActualizar_CreaNuevaVersionSinSobrescribir` (P8/C9), `TestActualizar_ConUsuarioId` (round-trip de `usuario_id`, P1), `TestListarPorLote` (C4). Se saltan sin `CATALOGOS_TEST_DB`.
- `migrations/grants_integration_test.go`: se agrega la aserción de que `svc_catalogos` puede cerrar la vigencia de un custodio (UPDATE `vigente_hasta`) y NO puede reescribir una versión (UPDATE `nombre` denegado, P8).
- `README.md`: pin a `tracer.8`; `custodios` pasa de scaffolding a implementado; tabla de brechas y pendientes de modelado de E5-T2.

**Decisiones de modelado (reportadas, pendientes de Operación/Lead):**
- `GET /custodios/{id}/aptitud` recibe `cliente_id` obligatorio por contrato (simetría con vehículo), pero el custodio no pertenece a un cliente: el parámetro se acepta y **no acota** la evaluación. La aptitud es por `estado` (DEC-055, informativa).
- El contrato expone `telefono`; se agregó la columna. Documentos de aptitud del custodio quedan "Por confirmar" (E5-HU-022): no se crea tabla sin consumidor.

**Verificado:** `go build ./...`, `go vet ./...` limpios; `arch-test@v0.1.1` OK (catalogos sigue hoja). Pruebas de integración ejecutadas contra Postgres 18 real (Docker): `custodios`, `vehiculos` y `TestGrants` en verde. `go build ./cmd/api` OK (I5 standalone).

**Pendiente:** C5, C8 y C10 siguen abiertas (otras historias). `cmd/api` aún no aplica migraciones al arrancar (TODO).

**Archivos modificados:**
- `go.mod`, `go.sum`
- `catalogos.go`
- `internal/custodios/custodios.go`
- `internal/custodios/custodios_test.go`
- `internal/custodios/custodios_integracion_test.go`
- `migrations/catalogos/0005_custodio_cols.sql`
- `migrations/grants_integration_test.go`
- `README.md`

## 2026:10:13 (E5-T1 — superficie de vehículos sobre tracer.6)

**Resumen:** Se implementa la superficie completa del sub-módulo `vehiculos` (E5-T1 / AI27-73) sobre el contrato `v0.1.0-tracer.6`, que cerró las brechas C3/C4/C6/C7/C9 de catálogos. Lectura paginada y por lote, lectura vigente o a una fecha con `version`, aptitud informativa, y escritura administrativa `POST`/`PATCH` que versiona por vigencia (no sobrescribe, P8) y emite `catalogo.actualizado` en la misma transacción (P17).

**Cambios:**
- `go.mod`: pin de `api-client-go` subido a `v0.1.0-tracer.6` (sin `replace` local).
- `migrations/catalogos/0003_vehiculo.sql` y `0004_vehiculo_grants.sql`: tablas `vehiculo` y `vehiculo_documento` versionadas por vigencia (PK `(id, version)`, índice único parcial por vigencia, índice `(cliente_id, estado)` y por vigencia de documento), con grants de privilegio mínimo (SELECT/INSERT, UPDATE solo de `vigente_hasta`, sin DELETE).
- `internal/vehiculos/vehiculos.go`: repositorio con `Listar` (cursor), `ListarPorLote` (C4), `Obtener` (vigente o a una fecha, C9), `Aptitud` (informativa, DEC-055), `Crear` y `Actualizar` (C7) con `pgx.BeginFunc` + outbox en la misma tx (P17). `escanearVehiculos` mapea `version` y vigencia (C6). Las columnas de BD son `vigente_desde`/`vigente_hasta`; el contrato las expone como `vigencia_*`.
- `catalogos.go`: `ConsultasAPI` ampliada (`ListarVehiculosPorLote`, `ObtenerVehiculo`); nueva `ComandosAPI` (`CrearVehiculo`, `ActualizarVehiculo`) expuesta por `Comandos()`; `Routes()` monta `GET /vehiculos` (con `?ids` para lote, excluyente con `cursor`), `POST /vehiculos`, `GET /vehiculos/{id}` (con `?a_fecha`), `PATCH /vehiculos/{id}` y la aptitud; helpers `parsearIDs` y `parsearAFecha`. Traducción de `ErrNoEncontrado` a 404 (I3).
- `internal/vehiculos/vehiculos_test.go`: 4 pruebas unitarias de aptitud (fake de `pgx.Row`).
- `internal/vehiculos/vehiculos_integracion_test.go` (nuevo): integración contra Postgres — `TestCrear_EmiteEventoEnLaMismaTx` (P17), `TestActualizar_CreaNuevaVersionSinSobrescribir` (P8/C9), `TestListarPorLote` (C4). Se saltan sin `CATALOGOS_TEST_DB`.
- `migrations/grants_integration_test.go`: aserciones de la tabla `vehiculo`.
- `README.md`, `internal/vehiculos/doc.go`, `migrations/catalogos/0003_vehiculo.sql`: documentación actualizada a `tracer.6` (brechas C3/C4/C6/C7/C9 cerradas e implementadas).

**Verificado:** `go build ./...`, `go vet ./...`, `gofmt` limpio en archivos nuevos, `arch-test@v0.1.1` OK (catalogos sigue hoja). Pruebas de integración ejecutadas contra Postgres 16 real (Docker): 7/7 de `vehiculos` + `TestGrants` en verde. Un bug de nombre de columna (`vigencia_desde` vs `vigente_desde`) se detectó y corrigió gracias a esas pruebas.

**Pendiente:** C5, C8 y C10 siguen abiertas (otras historias). `cmd/api` aún no aplica migraciones al arrancar (TODO).

**Archivos modificados:**
- `go.mod`, `go.sum`
- `catalogos.go`
- `internal/vehiculos/doc.go`
- `internal/vehiculos/vehiculos.go`
- `internal/vehiculos/vehiculos_test.go`
- `internal/vehiculos/vehiculos_integracion_test.go`
- `migrations/catalogos/0003_vehiculo.sql`
- `migrations/catalogos/0004_vehiculo_grants.sql`
- `migrations/grants_integration_test.go`
- `README.md`

## 2026:10:07 16:25

**Resumen:** Se exporta `PresupuestoRevocacion` para la arista restringida a `identidad` (RevocacionPort), por la obligación I3. Brecha detectada al cablear el prompt 09: el adaptador EN PROCESO de `pf-svc-core` para la revocación no tenía de dónde leer el presupuesto, porque el consumidor no lo exportaba.

**Cambios:**
- `catalogos.go`: se agrega `PresupuestoRevocacion` (`2 * time.Second`) exportado, junto al puerto `RevocacionPort`. `core` lo lee de aquí para el adaptador en proceso, en vez de duplicar el número (I3, ADR 17). El comentario distingue este presupuesto (una llamada puntual) del umbral de frescura del canal (15 s / 30 s, ADR 26). Se agrega el import de `time`.

**Verificado:** gofmt limpio, `go vet ./...`, `go build ./...`, `go test ./...`, build `-mod=readonly`, arch-test@v0.1.1 OK.

**Archivos modificados:**
- `catalogos.go`

## 2026:10:06 12:55

**Resumen:** Se reemplazó la plantilla de Pull Request (venía copiada del frontend) por una adaptada al stack Go de AI27.

**Cambios:**
- Se reescribió `.github/pull_request_template.md`: la anterior era la del portal-cliente (comandos pnpm, invariantes de UI, librerías prohibidas de frontend), que no aplica a un repo Go. La nueva cubre invariantes de arquitectura (I1-I5, contrato primero), base de datos, eventos/bus, verificación (go build/vet/test, arch-test, gofmt), CHANGELOG y convenciones.

**Archivos modificados:**
- `.github/pull_request_template.md`

## 2026:10:06 11:45

**Resumen:** Se corrigió el workflow de CI para autenticar siempre contra los repos privados de la organización.

**Cambios:**
- El paso que configuraba las credenciales de git tenía la guarda `if: ${{ env.GH_PAT != '' }}`, pero `GH_PAT` solo estaba definido en el `env` de ese mismo paso; el `if` se evalúa antes de aplicar ese `env`, así que la condición era siempre falsa y el paso se saltaba, dejando a `go mod download` sin credenciales (error 403 / "could not read Username").
- Se promovió `GH_PAT` al `env` del job (visible para los pasos) y se configuró el acceso sin guarda, con un paso previo que falla con un mensaje claro si el secreto no está.
- El secreto `GH_PAT` es a nivel de repo (requerido en el plan Free: los secretos de organización no llegan a repos privados).

**Archivos modificados:**
- `.github/workflows/ci.yml`

## 2026:10:06 10:13

**Resumen:** Esqueleto inicial del módulo `catalogos` (prompt 02, ADR 31, DEC-050). Hoja del grafo; sin lógica de negocio real todavía. Contrato fijado en `pf-pkg-contracts` v0.1.0-tracer.3.

**Cambios:**
- `go.mod`: requiere solo `pf-pkg-contracts/api-client-go` v0.1.0-tracer.3 y `pf-pkg-platform-go` v0.1.0; sin otro `pf-svc-*`, sin `replace` ni `go.work`.
- `catalogos.go`: superficie pública `New(Pools, Deps)`, `Routes`, `Workers` y `Consultas()`, tipada con tipos del contrato, exponiendo solo los `operationId` presentes en tracer.3 (lectura de cliente/contrato/SLA y aptitud informativa de custodio/"unidad").
- `ports.go`: declara la ausencia de aristas de dominio (hoja); solo la arista restringida a `identidad` para revocación (DEC-051/058).
- `migrations/catalogos/0001_init.sql`: esquema `catalogos`, rol `svc_catalogos` y su único GRANT, `outbox`, `inbox_evento`, y las tablas desbloqueadas (cliente, contrato, sla, custodio) versionadas por vigencia. Sin tabla `unidad`, disponibilidad ni reserva (ADR 31). Campos reales como TODO, no inventados.
- `internal/outbox.go`: escribe `catalogo.actualizado` (tipo + identidad + versión, nunca payload) en la misma transacción que la mutación, con prueba.
- Sub-módulos `clientes`, `sla`, `vehiculos`, `custodios`, `tipos` (doc + alcance); `rutas` y `parametros` solo declaran su diferimiento a V2.
- `cmd/api`: arranque standalone (I5) con `/health` y `/ready` y pool de `svc_catalogos`.
- CI y `tools.go` (fija las dependencias del `arch-test` que corre por `go run`).
- Brechas de contrato reportadas (ola 1 / tracer.4): C3 (átomo vehículo aún modelado como `unidades` de ADR 24), C4/C5 (lecturas por lote), C6 (versión en lecturas), C7 (admin POST/PATCH), C8 (catálogos de tipo), C9 (lectura a una fecha de átomos), C10 (versión en `catalogo.actualizado`).

**Archivos modificados:**
- `go.mod`
- `go.sum`
- `.gitignore`
- `README.md`
- `catalogos.go`
- `ports.go`
- `tools.go`
- `cmd/api/main.go`
- `internal/clientes/doc.go`
- `internal/sla/doc.go`
- `internal/vehiculos/doc.go`
- `internal/custodios/doc.go`
- `internal/tipos/doc.go`
- `internal/rutas/doc.go`
- `internal/parametros/doc.go`
- `internal/outbox.go`
- `internal/outbox_test.go`
- `migrations/catalogos/0001_init.sql`
- `migrations/migrations.go`
- `.github/workflows/ci.yml`
