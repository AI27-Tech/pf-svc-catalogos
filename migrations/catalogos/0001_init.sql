-- 0001_init.sql — esquema, rol, outbox y tablas iniciales de catalogos.
-- Un solo esquema `catalogos` y un solo rol `svc_catalogos` (00 regla 4,
-- catalogos.md (d)). Sin ningún GRANT hacia este esquema fuera de svc_catalogos
-- (invariante I1: aislamiento por rol). Sin FK hacia otro esquema (SPEC §A.5 R1).
--
-- Toda entidad se versiona por VIGENCIA, nunca se sobrescribe ni se borra en
-- firme (P8, catalogos.md (e)): la exportación de un viaje cerrado hace un año
-- necesita el contrato y el SLA vigentes entonces.

CREATE SCHEMA IF NOT EXISTS catalogos;

-- Rol dueño del esquema. LOGIN para que el pool se conecte con él.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'svc_catalogos') THEN
    CREATE ROLE svc_catalogos LOGIN;
  END IF;
END
$$;

GRANT USAGE ON SCHEMA catalogos TO svc_catalogos;
ALTER DEFAULT PRIVILEGES IN SCHEMA catalogos
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO svc_catalogos;

-- ---------------------------------------------------------------------------
-- outbox — capa de dominio del sobre (refleja outbox.OutboxDDL de
-- pf-pkg-platform-go). El relay la drena y publica catalogo.actualizado tras el
-- acuse del transporte (ADR 29). El evento lleva tipo + identidad + versión,
-- nunca el payload (catalogos.md (g)); va en la MISMA tx que la nueva versión.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.outbox (
  id            BIGSERIAL   PRIMARY KEY,
  evento_id     UUID        NOT NULL UNIQUE,
  tipo          TEXT        NOT NULL,
  version       INT         NOT NULL DEFAULT 1,
  agregado_id   TEXT        NOT NULL,
  sensibilidad  TEXT        NOT NULL DEFAULT 'amplia',
  ocurrido_en   TIMESTAMPTZ NOT NULL,
  payload       JSONB       NOT NULL,
  trace_context JSONB       NOT NULL DEFAULT '{}'::jsonb,
  publicado     BOOLEAN     NOT NULL DEFAULT false,
  publicado_en  TIMESTAMPTZ,
  creado_en     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS outbox_pendientes_idx
  ON catalogos.outbox (creado_en) WHERE publicado = false;

-- ---------------------------------------------------------------------------
-- inbox_evento — deduplicación idempotente del consumidor de sesion.revocada
-- (refleja inbox.InboxDDL). El consumidor registra el evento_id en la misma tx
-- que aplica el tombstone; un duplicado no produce un segundo efecto (ADR 29 §1).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.inbox_evento (
  evento_id    UUID        PRIMARY KEY,
  tipo         TEXT        NOT NULL,
  consumidor   TEXT        NOT NULL,
  procesado_en TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- cliente — expediente comercial. Versionado por vigencia (catalogos.md (e)).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.cliente (
  id             UUID        NOT NULL,
  version        INT         NOT NULL,
  nombre         TEXT        NOT NULL,
  estado         TEXT        NOT NULL,
  vigente_desde  TIMESTAMPTZ NOT NULL DEFAULT now(),
  vigente_hasta  TIMESTAMPTZ,
  PRIMARY KEY (id, version)
  -- TODO(catalogos): campos reales del expediente, pendientes de Operación y de
  -- la superficie admin del contrato (brecha C7). No inventar columnas.
);
-- Solo una versión vigente por cliente.
CREATE UNIQUE INDEX IF NOT EXISTS un_cliente_vigente
  ON catalogos.cliente (id) WHERE vigente_hasta IS NULL;

-- ---------------------------------------------------------------------------
-- contrato — contrato del cliente, versionado por vigencia.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.contrato (
  id             UUID        NOT NULL,
  version        INT         NOT NULL,
  cliente_id     UUID        NOT NULL,
  estado         TEXT        NOT NULL,
  vigente_desde  TIMESTAMPTZ NOT NULL DEFAULT now(),
  vigente_hasta  TIMESTAMPTZ,
  PRIMARY KEY (id, version)
  -- Sin FK a cliente dentro del mismo esquema para no acoplar versiones; la
  -- integridad se valida en el caso de uso. TODO(catalogos): términos reales.
);
CREATE UNIQUE INDEX IF NOT EXISTS un_contrato_vigente_por_cliente
  ON catalogos.contrato (cliente_id) WHERE vigente_hasta IS NULL;

-- ---------------------------------------------------------------------------
-- sla — compromisos por contrato. INFORMATIVO en V1: ningún flujo lo consume
-- para bloquear ni alertar (DEC-022 #4). Versionado por vigencia.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.sla (
  id             UUID        NOT NULL,
  version        INT         NOT NULL,
  contrato_id    UUID        NOT NULL,
  vigente_desde  TIMESTAMPTZ NOT NULL DEFAULT now(),
  vigente_hasta  TIMESTAMPTZ,
  PRIMARY KEY (id, version)
  -- TODO(catalogos): compromisos reales (sobre todo de seguridad), pendientes.
);
CREATE UNIQUE INDEX IF NOT EXISTS un_sla_vigente_por_contrato
  ON catalogos.sla (contrato_id) WHERE vigente_hasta IS NULL;

-- ---------------------------------------------------------------------------
-- custodio — ÁTOMO: persona custodio (ADR 31 §5). NO se clasifica (sin "tipo de
-- custodia", E5-HU-022). Referencia al usuario de identidad por IDENTIFICADOR,
-- nunca FK (SPEC §A.5 R1). Versionado por vigencia.
--
-- LFPDPPP: el derecho ARCO de cancelación del titular convive con la retención
-- probatoria de P8; la resolución queda pendiente (catalogos.md (e)). No se
-- resuelve en el scaffold.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.custodio (
  id              UUID        NOT NULL,
  version         INT         NOT NULL,
  nombre          TEXT        NOT NULL,
  estado          TEXT        NOT NULL,
  usuario_id      TEXT,       -- identificador del usuario de identidad, si existe; NO es FK
  vigente_desde   TIMESTAMPTZ NOT NULL DEFAULT now(),
  vigente_hasta   TIMESTAMPTZ,
  PRIMARY KEY (id, version)
  -- TODO(catalogos): placas/documentos/corto/económico y documentos de aptitud,
  -- "Por confirmar" en E5-HU-022. No inventar columnas.
);
CREATE UNIQUE INDEX IF NOT EXISTS un_custodio_vigente
  ON catalogos.custodio (id) WHERE vigente_hasta IS NULL;
CREATE INDEX IF NOT EXISTS custodio_por_estado_idx
  ON catalogos.custodio (estado) WHERE vigente_hasta IS NULL;

-- ---------------------------------------------------------------------------
-- BLOQUEADO POR BRECHA DE CONTRATO — NO SE CREA EN ESTE SCAFFOLD:
--   * vehiculo (átomo coche/moto, ADR 31)        -> brecha C3 (contrato aún en ADR 24 "unidades")
--   * catálogos de tipo (tipos, líneas, GPS, ...) -> brecha C8
-- DIFERIDO A V2 (ADR 20 / DEC-038) — NO SE CREA:
--   * ruta / geometría de zonas de riesgo
--   * parametro_operativo (forma bloqueada por ADR 5)
--   * kit de seguridad, tipos de custodia (DEC-047)
-- ---------------------------------------------------------------------------
