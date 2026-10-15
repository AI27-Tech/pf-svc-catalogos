-- 0008_contrato_sla_cols.sql — columnas de contrato y sla para E5-T4 (AI27-76).
-- Desbloqueada por el contrato v0.1.0-tracer.8: los schemas Contrato y Sla ya
-- exponen `descripcion` (informativo en V1, DEC-022 #4 / DEC-025). Las tablas y
-- sus índices únicos parciales de vigencia ya existen desde 0001_init.sql
-- (un_contrato_vigente_por_cliente, un_sla_vigente_por_contrato); aquí solo se
-- alinean las columnas con el contrato que aterrizó.
--
-- Versionado por VIGENCIA, igual que cliente/vehiculo/custodio: una nueva
-- versión es un INSERT; cerrar la anterior es fijar su vigente_hasta. Nunca se
-- sobrescribe ni se borra en firme (P8): la exportación probatoria de un viaje
-- cerrado hace un año necesita el contrato/SLA tal como regía entonces.
--
-- INFORMATIVO en V1: ningún flujo de E1 (crear, aprobar, asignar, cerrar) lee
-- contrato ni SLA para bloquear o alertar (DEC-022 #4, confirmado por Max en
-- DEC-025). Estas columnas existen para lectura administrativa y exportación
-- probatoria (P8), no para condicionar el servicio.

-- ---------------------------------------------------------------------------
-- contrato — alinear con el schema Contrato (tracer.8).
-- ---------------------------------------------------------------------------
-- El contrato expone `descripcion` (informativo). La agrega como opcional.
ALTER TABLE catalogos.contrato ADD COLUMN IF NOT EXISTS descripcion TEXT;

-- 0001 creó `estado NOT NULL`, pero el schema Contrato del contrato NO lo
-- expone: en V1 el contrato no tiene estado administrado por la API. Se relaja a
-- NULL para que el alta (POST) no dependa de un campo que el contrato no entrega;
-- queda como columna reservada, no gobernada por V1. No se elimina para no
-- perder el dato de las filas que ya lo tuvieran.
ALTER TABLE catalogos.contrato ALTER COLUMN estado DROP NOT NULL;

-- ---------------------------------------------------------------------------
-- sla — alinear con el schema Sla (tracer.8).
-- ---------------------------------------------------------------------------
-- El contrato expone `descripcion` (compromiso informativo en V1). La tabla de
-- 0001 no la tenía. La agrega como opcional.
ALTER TABLE catalogos.sla ADD COLUMN IF NOT EXISTS descripcion TEXT;
