-- 0007_valor_catalogo_grants.sql — privilegio mínimo de la tabla de 0006 (E0-T5).
-- Fase de grants separada de la de estructura, igual que 0004 lo es de 0003.
--
-- Por qué existe por separado: 0002 retiró el ALTER DEFAULT PRIVILEGES genérico
-- de 0001, así que una tabla creada después (0006: valor_catalogo) NO hereda
-- ningún privilegio. Cada tabla nueva declara aquí sus GRANT explícitos y se
-- agrega a grants_integration_test.go (nota de 0002).
--
-- Criterio idéntico al de 0002/0004: SELECT sobre lo propio; una nueva versión
-- es un INSERT; cerrar la versión anterior es un UPDATE restringido a
-- vigente_hasta; DELETE en ninguna tabla (P8), y ninguna otra columna es
-- actualizable (no se reescribe una versión — desactivar un valor crea una
-- versión nueva, no muta la vigente). Ningún GRANT hacia este esquema fuera de
-- svc_catalogos (invariante I1, §A.5 R1).
--
-- Idempotente: reaplicarla deja el mismo estado.

-- --- valor_catalogo: versionado por vigencia (P8) ------------------------------
GRANT SELECT, INSERT         ON catalogos.valor_catalogo TO svc_catalogos;
GRANT UPDATE (vigente_hasta) ON catalogos.valor_catalogo TO svc_catalogos;
