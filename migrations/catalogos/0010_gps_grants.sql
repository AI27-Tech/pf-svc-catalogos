-- 0010_gps_grants.sql — privilegio mínimo de la tabla de 0009 (E0-T5).
-- 0002 retiró el ALTER DEFAULT PRIVILEGES genérico, así que catalogos.gps no
-- hereda nada. En V1 el servicio solo LEE el catálogo (listarGps): SELECT y
-- nada más. Ningún GRANT fuera de svc_catalogos (I1). Idempotente.
GRANT SELECT ON catalogos.gps TO svc_catalogos;
