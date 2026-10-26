-- 0005_custodio_cols.sql — columnas de atributos del custodio para E5-T2.
-- La tabla catalogos.custodio nació en 0001 con el mínimo (id, version, nombre,
-- estado, usuario_id, vigencias). El contrato de custodio (tracer.6+) expone
-- además `telefono` como dato de contacto de la persona. Esta migración lo
-- agrega para que la superficie administrativa (POST/PATCH, E5-T2) pueda
-- persistir lo que el contrato ya declara.
--
-- Por qué solo `telefono`: el TODO de 0001 dejaba "placas/documentos/aptitud"
-- como "Por confirmar" en E5-HU-022. El contrato de V1 solo expone `telefono`
-- (y `usuario_id`, que ya existe). No se inventan columnas de documentos ni de
-- aptitud mientras no haya decisión de Operación: la aptitud del custodio en V1
-- se evalúa solo por `estado` (DEC-055, informativa).
--
-- NO requiere fase de grants nueva: 0002 ya otorgó SELECT, INSERT y
-- UPDATE (vigente_hasta) sobre catalogos.custodio, y esos privilegios son a
-- nivel de tabla (cubren toda columna presente y futura, salvo el UPDATE, que
-- sigue restringido a vigente_hasta — una versión no se reescribe, P8).
--
-- Idempotente: ADD COLUMN IF NOT EXISTS.

ALTER TABLE catalogos.custodio
  ADD COLUMN IF NOT EXISTS telefono TEXT;
