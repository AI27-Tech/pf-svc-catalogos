# CHANGELOG

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
