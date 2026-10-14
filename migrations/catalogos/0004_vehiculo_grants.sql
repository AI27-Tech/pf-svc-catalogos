-- 0004_vehiculo_grants.sql — privilegio mínimo de las tablas de 0003 (E0-T5).
-- Fase de grants separada de la de estructura, igual que 0002 lo es de 0001.
--
-- Por qué existe por separado: 0002 retiró el ALTER DEFAULT PRIVILEGES genérico
-- de 0001, así que una tabla creada después (0003: vehiculo, vehiculo_documento)
-- NO hereda ningún privilegio. Cada tabla nueva declara aquí sus GRANT
-- explícitos y se agrega a grants_integration_test.go (nota de 0002).
--
-- Criterio idéntico al de 0002: SELECT sobre lo propio; una nueva versión es un
-- INSERT; cerrar la versión anterior es un UPDATE restringido a vigente_hasta;
-- DELETE en ninguna tabla (P8), y ninguna otra columna es actualizable (no se
-- reescribe una versión). Ningún GRANT hacia este esquema fuera de
-- svc_catalogos (invariante I1, §A.5 R1).
--
-- Idempotente: reaplicarla deja el mismo estado.

-- --- vehiculo: versionado por vigencia (P8) ------------------------------------
GRANT SELECT, INSERT         ON catalogos.vehiculo TO svc_catalogos;
GRANT UPDATE (vigente_hasta) ON catalogos.vehiculo TO svc_catalogos;

-- --- vehiculo_documento: versionado por vigencia (P8) --------------------------
GRANT SELECT, INSERT         ON catalogos.vehiculo_documento TO svc_catalogos;
GRANT UPDATE (vigente_hasta) ON catalogos.vehiculo_documento TO svc_catalogos;
