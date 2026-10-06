// Package internal reúne piezas compartidas entre los sub-módulos de catalogos
// que no son de un sub-módulo concreto. Aquí vive el helper que escribe el único
// evento del servicio, catalogo.actualizado, en la outbox.
package internal

import (
	"context"
	"fmt"

	"github.com/AI27-Tech/pf-pkg-platform-go/outbox"
	"github.com/jackc/pgx/v5"
)

// TipoCatalogoActualizado es el tipo del único evento que publica catalogos.
// Debe estar en el registro de tipos del contrato; el payload NUNCA se publica,
// solo tipo + identidad + versión (catalogos.md (g)).
const TipoCatalogoActualizado = "catalogo.actualizado"

// EntidadActualizada identifica qué cambió: el tipo de entidad (p. ej.
// "cliente", "contrato", "sla", "custodio"), su identificador y la versión nueva.
type EntidadActualizada struct {
	Entidad   string // tipo de entidad de catálogo
	EntidadID string // identificador de la entidad
	Version   int    // versión nueva, para orden y trazabilidad del consumidor
}

// PublicarActualizacion escribe catalogo.actualizado en la outbox dentro de la
// MISMA transacción que la nueva versión de la entidad (P17). El llamador hace
// commit: el cambio y el evento se comprometen juntos. El relay (Workers) lo
// drena y publica tras el acuse del transporte.
//
// La clave de orden (AgregadoID) es la identidad de la entidad, para que las
// actualizaciones de una misma entidad lleguen en orden al consumidor.
//
// BRECHA C10: el proto CatalogoActualizado del contrato tracer.3 todavía no
// lleva un campo de versión; aquí la versión viaja en la capa de dominio del
// sobre (outbox.Event.Version). Cuando el contrato agregue el campo, el payload
// se alineará. No se escribe el payload del evento (catalogos.md (g)).
func PublicarActualizacion(ctx context.Context, pub *outbox.Publisher, tx pgx.Tx, e EntidadActualizada) error {
	if e.Entidad == "" || e.EntidadID == "" {
		return fmt.Errorf("catalogos: entidad y entidad_id son obligatorios para catalogo.actualizado")
	}
	return pub.Publish(ctx, tx, outbox.Event{
		Tipo:         TipoCatalogoActualizado,
		Version:      e.Version,
		AgregadoID:   e.EntidadID,
		Sensibilidad: outbox.SensibilidadAmplia,
		// Payload mínimo: tipo + identidad, nunca el contenido de la entidad.
		Payload: map[string]string{
			"entidad":    e.Entidad,
			"entidad_id": e.EntidadID,
		},
	})
}
