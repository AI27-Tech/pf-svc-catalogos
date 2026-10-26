-- 0003_vehiculo.sql — tabla del ÁTOMO vehículo de AI27 (ADR 31 §5, E5-HU-021).
-- Desbloqueada por el contrato v0.1.0-tracer.6: el átomo se renombró de
-- /api/v1/unidades (modelo viejo ADR 24) a /api/v1/vehiculos. En tracer.3/4
-- este archivo no podía existir (brecha C3, cerrada en la ola de catálogos).
--
-- `vehiculo` es un ÁTOMO: coche o moto. La `unidad` (paquete vehículo +
-- custodios), su roster, disponibilidad y reserva son de `operacion`, NO de
-- aquí (ADR 31). Por eso NO hay columna `disponible` ni `reservado`: catalogos
-- responde "qué es y si es apto", no "si está libre". `patrulla`/`moto` son
-- TIPOS DE UNIDAD que `operacion` deriva del `tipo` del vehículo; nunca columna
-- aquí.
--
-- Versionado por VIGENCIA, igual que cliente/contrato/sla/custodio en 0001: una
-- nueva versión es un INSERT; cerrar la anterior es fijar su vigente_hasta.
-- Nunca se sobrescribe ni se borra en firme (P8): la exportación probatoria de
-- un viaje cerrado hace un año necesita el vehículo tal como estaba entonces.
--
-- CONTRATO (tracer.6), schema Vehiculo: id, tipo (coche|moto), economico,
-- placas, estado (activa|inactiva). Esas son las columnas que una lectura
-- expone HOY. Los documentos y sus vigencias sostienen la aptitud informativa
-- (DEC-055) y NO viajan en el schema de lectura todavía; se modelan aquí para
-- que el barrido de aptitud (documento vencido ⇒ no apto) tenga de dónde salir.

-- ---------------------------------------------------------------------------
-- vehiculo — átomo coche/moto, versionado por vigencia.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.vehiculo (
  id             UUID        NOT NULL,
  version        INT         NOT NULL,
  -- Identidad del átomo (contrato tracer.6):
  tipo           TEXT        NOT NULL,   -- 'coche' | 'moto' (enum validado en el caso de uso)
  economico      TEXT,                   -- número económico, opcional en el contrato
  placas         TEXT,                   -- opcional en el contrato
  -- Estado administrativo. El contrato expone 'activa' | 'inactiva'.
  estado         TEXT        NOT NULL,
  -- Cliente al que pertenece el vehículo, para el índice (cliente_id, estado)
  -- que pide la historia. Referencia por IDENTIFICADOR, nunca FK a otro
  -- esquema (SPEC §A.5 R1); aquí es dentro de catalogos pero se deja sin FK a
  -- cliente para no acoplar versiones.
  cliente_id     UUID        NOT NULL,
  vigente_desde  TIMESTAMPTZ NOT NULL DEFAULT now(),
  vigente_hasta  TIMESTAMPTZ,
  PRIMARY KEY (id, version)
);

-- Solo una versión vigente por vehículo (mismo patrón que un_cliente_vigente).
CREATE UNIQUE INDEX IF NOT EXISTS un_vehiculo_vigente
  ON catalogos.vehiculo (id) WHERE vigente_hasta IS NULL;

-- Índice por (cliente_id, estado) sobre la versión vigente — la historia lo pide
-- de forma explícita para el listado por cliente.
CREATE INDEX IF NOT EXISTS vehiculo_por_cliente_estado_idx
  ON catalogos.vehiculo (cliente_id, estado) WHERE vigente_hasta IS NULL;

-- ---------------------------------------------------------------------------
-- vehiculo_documento — documentos del vehículo con su vigencia. Sostienen la
-- APTITUD INFORMATIVA (DEC-055): un documento vencido ⇒ el vehículo se reporta
-- "no apto", pero catalogos NUNCA bloquea la asignación; `operacion` decide.
-- Versionado por vigencia como todo lo demás (P8).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.vehiculo_documento (
  id             UUID        NOT NULL,
  version        INT         NOT NULL,
  vehiculo_id    UUID        NOT NULL,   -- identificador del vehículo; sin FK para no acoplar versiones
  clase          TEXT        NOT NULL,   -- clase de documento (p. ej. 'poliza', 'verificacion')
  vence_el       TIMESTAMPTZ,            -- fecha de vencimiento; NULL = sin vencimiento
  vigente_desde  TIMESTAMPTZ NOT NULL DEFAULT now(),
  vigente_hasta  TIMESTAMPTZ,
  PRIMARY KEY (id, version)
);

-- Barrido de aptitud: documentos vigentes de un vehículo, ordenables por
-- vencimiento. Índice por vigencia de documento, como pide la historia.
CREATE INDEX IF NOT EXISTS vehiculo_documento_por_vehiculo_idx
  ON catalogos.vehiculo_documento (vehiculo_id, vence_el) WHERE vigente_hasta IS NULL;
