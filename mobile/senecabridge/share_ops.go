package senecabridge

import (
	"errors"

	"github.com/sglavoie/seneca/engine"
)

// Share packets: what the author sends to another person, assembled by the
// engine. The app previews exactly this text and hands it to the share sheet;
// nothing is sent from here.

type sharePacketParams struct {
	// ReviewID is the saved review to share, empty for none.
	ReviewID string `json:"review_id"`
	// Dates are the days whose entries go with it.
	Dates []string `json:"dates"`
}

type sharePacketResult struct {
	Title    string   `json:"title"`
	FileName string   `json:"file_name"`
	Markdown string   `json:"markdown"`
	ReviewID string   `json:"review_id"`
	Dates    []string `json:"dates"`
}

func sharePacket(c *call) (any, error) {
	p, err := params[sharePacketParams](c)
	if err != nil {
		return nil, err
	}
	packet, err := c.journal.SharePacket(p.ReviewID, p.Dates)
	var selection *engine.ShareSelectionError
	switch {
	case errors.As(err, &selection) && selection.NotFound:
		return nil, refuse("not-found", "%s", selection.Error())
	case errors.As(err, &selection):
		return nil, refuse("invalid-query", "%s", selection.Error())
	case err != nil:
		return nil, err
	}
	return sharePacketResult{
		Title: packet.Title, FileName: packet.FileName, Markdown: packet.Markdown,
		ReviewID: packet.ReviewID, Dates: append([]string{}, packet.Dates...),
	}, nil
}
