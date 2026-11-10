-- 0012_cliente_contacto.sql — datos de contacto de la organización cliente (DEC-139 #2, DEC-144 #2).
--
-- El contrato tracer.40 da de alta el cliente con nombre, correo y teléfono, los
-- tres obligatorios. Las columnas son NULL porque los clientes dados de alta antes
-- de tracer.40 no los tienen (el schema `Cliente` los declara opcionales en la
-- respuesta). La obligatoriedad la valida el alta, no la tabla.
--
-- NO requiere fase de grants nueva: 0002 otorgó SELECT e INSERT sobre
-- catalogos.cliente a nivel de tabla (cubren estas columnas) y el UPDATE sigue
-- limitado a vigente_hasta, así que una versión no se reescribe (P8).
--
-- Idempotente: ADD COLUMN IF NOT EXISTS.
ALTER TABLE catalogos.cliente
  ADD COLUMN IF NOT EXISTS correo   TEXT,
  ADD COLUMN IF NOT EXISTS telefono TEXT;
