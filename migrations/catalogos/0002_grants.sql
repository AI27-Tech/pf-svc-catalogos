-- 0002_grants.sql — privilegio mínimo por tabla del esquema catalogos (E0-T5).
-- Fase de grants, separada de la de estructura (0001), igual que en operacion.
--
-- NOTA PARA E5 (Gonzalo): esta migración la agregó el Lead como parte de E0-T5.
-- Toda tabla nueva de catalogos (0003+) NO hereda privilegios: declara sus GRANT
-- explícitos en su propia fase de grants y se agrega a grants_integration_test.go.
--
-- Por qué existe: 0001 deja un ALTER DEFAULT PRIVILEGES genérico que otorga
-- SELECT, INSERT, UPDATE y DELETE sobre toda tabla, y ningún privilegio sobre
-- secuencias. Medido sobre Postgres 18: svc_catalogos podía borrar versiones de
-- catalogos.cliente (contradice P8) y NO podía insertar en catalogos.outbox
-- ("permission denied for sequence outbox_id_seq"), es decir, no podía publicar
-- catalogo.actualizado. El invariante I1 (ADR 17) solo es real con privilegio
-- mínimo: el rol puede lo que su caso de uso necesita y nada más.
--
-- Criterio: SELECT sobre lo propio; escritura solo según el flujo documentado;
-- DELETE en ninguna tabla. Ningún GRANT hacia este esquema fuera de
-- svc_catalogos y ningún GRANT de svc_catalogos sobre otro esquema (§A.5 R1).
--
-- La aplica el MISMO rol que aplicó 0001: ALTER DEFAULT PRIVILEGES ... REVOKE
-- solo retira los defaults que ese rol creó. Es idempotente: reaplicarla deja
-- el mismo estado.

-- --- Partir de cero: retirar lo que 0001 otorgó de forma genérica --------------
REVOKE ALL ON ALL TABLES    IN SCHEMA catalogos FROM svc_catalogos;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA catalogos FROM svc_catalogos;
-- Una tabla futura (0003+) no hereda privilegios: cada migración que crea una
-- tabla declara sus GRANT explícitos en su propia fase de grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA catalogos
  REVOKE ALL ON TABLES FROM svc_catalogos;

-- --- Entidades versionadas por vigencia (P8, catalogos.md (e)) ------------------
-- Una nueva versión es un INSERT. Cerrar la versión anterior es fijar su
-- vigente_hasta. Nada más se sobrescribe ni se borra en firme: el UPDATE se
-- limita a esa columna, así la base rechaza reescribir una versión.
GRANT SELECT, INSERT              ON catalogos.cliente  TO svc_catalogos;
GRANT UPDATE (vigente_hasta)      ON catalogos.cliente  TO svc_catalogos;
GRANT SELECT, INSERT              ON catalogos.contrato TO svc_catalogos;
GRANT UPDATE (vigente_hasta)      ON catalogos.contrato TO svc_catalogos;
GRANT SELECT, INSERT              ON catalogos.sla      TO svc_catalogos;
GRANT UPDATE (vigente_hasta)      ON catalogos.sla      TO svc_catalogos;
GRANT SELECT, INSERT              ON catalogos.custodio TO svc_catalogos;
GRANT UPDATE (vigente_hasta)      ON catalogos.custodio TO svc_catalogos;

-- --- outbox: publicación de catalogo.actualizado (ADR 29) -----------------------
-- El publicador inserta en la misma tx que la nueva versión; el relay lee los
-- pendientes y marca publicado/publicado_en. No borra.
GRANT SELECT, INSERT, UPDATE ON catalogos.outbox TO svc_catalogos;
-- INSERT en una tabla BIGSERIAL exige USAGE sobre su secuencia.
GRANT USAGE ON SEQUENCE catalogos.outbox_id_seq TO svc_catalogos;

-- --- inbox_evento: deduplicación del consumidor de sesion.revocada --------------
-- Alta del evento_id en la misma tx que el efecto. Nunca se actualiza ni borra.
GRANT SELECT, INSERT ON catalogos.inbox_evento TO svc_catalogos;
