-- 0006_valor_catalogo.sql — catálogos de tipo de V1 (E5-T3, E5-HU-023).
-- Desbloqueada por el contrato v0.1.0-tracer.8: la superficie de catálogos de
-- tipo (brecha C8) se cerró con el recurso /api/v1/catalogos-tipo/{tipo}. En
-- tracer.3 esta tabla no podía existir (0001 la dejó anotada como bloqueada).
--
-- UNA sola tabla parametrizada por `tipo` para las CINCO listas de valores de
-- V1 (opción A del contrato): tipos_servicio, lineas_transporte, tipos_unidad
-- (incluye `Patrulla`, la unidad en su rol de escolta — ADR 31 / DEC-044),
-- tipos_custodia y zonas_riesgo. Comparten forma (código, etiqueta, estado,
-- versión), así que no se declaran cinco tablas casi idénticas. Agregar una
-- lista futura es un valor más del enum `tipo`, no una tabla nueva.
--
-- `zonas_riesgo` entra SOLO como lista de valores: SIN geometría (DEC-017). La
-- geometría, la segmentación por cliente y la temporalidad se difieren a V2
-- (ADR 5 / DEC-047); cuando V2 las agregue, zonas_riesgo se gradúa a su propio
-- recurso y no contamina esta tabla.
--
-- Versionado por VIGENCIA, igual que cliente/contrato/sla/custodio/vehiculo:
-- una nueva versión es un INSERT; cerrar la anterior es fijar su vigente_hasta.
-- Nunca se sobrescribe ni se borra en firme (P8). DESACTIVAR un valor es crear
-- una nueva versión con estado 'inactivo' (E5-HU-023): no rompe los registros
-- históricos que ya lo usaban, que siguen apuntando a la versión que regía
-- entonces (lectura "a una fecha", C9).
--
-- CONTRATO (tracer.8, schema ValorCatalogo): id, version, codigo, etiqueta,
-- estado (activo|inactivo). El schema de LECTURA no expone las vigencias (a
-- diferencia de Vehiculo/Custodio); se persisten igual para sostener la lectura
-- probatoria "a una fecha" (P8/C9) y el versionado. El `tipo` viaja en la ruta,
-- no en el cuerpo del contrato; aquí es una columna que discrimina la lista.
--
-- AUTORIZACIÓN POR ROL (DEC-047 #1): las líneas de transporte las crea sólo un
-- Coordinador. En V1 NO se implementa aquí (decisión A1 de la sesión): no hay
-- aún mecanismo de autorización por rol en catalogos, y el contrato no da
-- gancho por tipo. Queda anotado como pendiente; la escritura no distingue rol.

-- ---------------------------------------------------------------------------
-- valor_catalogo — un valor de cualquiera de las cinco listas de tipo de V1.
-- La identidad de una fila-versión es (id, version); la de un valor de negocio
-- dentro de su lista es (tipo, codigo).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS catalogos.valor_catalogo (
  id             UUID        NOT NULL,
  version        INT         NOT NULL,
  -- Cuál de las cinco listas de V1. Se valida contra el enum TipoCatalogo en el
  -- caso de uso (no un CHECK en la BD: agregar una lista futura no debe exigir
  -- una migración de constraint, solo un valor más del enum del contrato).
  tipo           TEXT        NOT NULL,
  -- Código estable del valor (máquina). No se traduce.
  codigo         TEXT        NOT NULL,
  -- Etiqueta legible del valor (presentación).
  etiqueta       TEXT        NOT NULL,
  -- Estado administrativo. El contrato expone 'activo' | 'inactivo'. Desactivar
  -- (inactivo) no rompe la historia (P8).
  estado         TEXT        NOT NULL,
  vigente_desde  TIMESTAMPTZ NOT NULL DEFAULT now(),
  vigente_hasta  TIMESTAMPTZ,
  PRIMARY KEY (id, version)
);

-- Solo una versión vigente por valor (mismo patrón que un_vehiculo_vigente).
CREATE UNIQUE INDEX IF NOT EXISTS un_valor_catalogo_vigente
  ON catalogos.valor_catalogo (id) WHERE vigente_hasta IS NULL;

-- Lectura por lote de una lista (C4): los valores vigentes de un `tipo`, sin
-- N+1. Es la consulta que sirve listarValoresCatalogo.
CREATE INDEX IF NOT EXISTS valor_catalogo_por_tipo_idx
  ON catalogos.valor_catalogo (tipo) WHERE vigente_hasta IS NULL;

-- Un código es único dentro de su lista entre las versiones vigentes: dos
-- valores vigentes de `lineas_transporte` no pueden compartir código. No acota
-- entre listas (el mismo código puede existir en tipos distintos) ni contra
-- versiones ya cerradas (P8: la historia conserva códigos retirados).
CREATE UNIQUE INDEX IF NOT EXISTS un_valor_catalogo_codigo_vigente
  ON catalogos.valor_catalogo (tipo, codigo) WHERE vigente_hasta IS NULL;
