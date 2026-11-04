-- 0009_gps.sql — catálogo de dispositivos GPS (DEC-047 #2, DEC-104 #5).
-- Desbloqueada por el contrato v0.1.0-tracer.37 (`listarGps`, `DispositivoGps`).
-- 0001 lo dejó anotado como bloqueado por la brecha C8.
--
-- La ficha técnica del viaje (operacion) referencia el dispositivo por `id`
-- (DEC-012), sin posición en vivo. El selector de la ficha ofrece solo los
-- `activo`; la referencia histórica se conserva aunque el dispositivo se
-- desactive, porque desactivar no borra la fila (DEC-104 #5).
--
-- CONTRATO (DispositivoGps): id, identificador, activo. Sin versión ni vigencias.
-- shortcut: tabla plana, sin versionado por vigencia (P8) como el resto de
-- catalogos: en V1 no hay superficie de escritura (la carga de los ~100
-- dispositivos es trabajo de datos de Operación, DEC-047). Si se agrega
-- alta/edición por API, pasa al patrón (id, version, vigente_desde, vigente_hasta).
--
-- Va DESPUÉS de 0002_grants (que retiró los privilegios por defecto): sus GRANT
-- van en 0010_gps_grants.sql. Idempotente.
CREATE TABLE IF NOT EXISTS catalogos.gps (
  id            UUID        PRIMARY KEY,
  -- Alias operativo del dispositivo (p. ej. "MOVIL ST7916").
  identificador TEXT        NOT NULL,
  activo        BOOLEAN     NOT NULL DEFAULT true,
  creado_en     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- La consulta de listarGps: los activos, en orden de id (cursor keyset).
CREATE INDEX IF NOT EXISTS gps_activos_idx
  ON catalogos.gps (id) WHERE activo;
