package lifecycle

import (
	"context"

	"github.com/fpresta0607/code-goblins/internal/verify"
)

func VerificationControllers(ctx context.Context) ([]Identity, error) {
	dir, err := verify.AdmissionDir()
	if err != nil {
		return nil, err
	}
	controllers, err := verify.Controllers(ctx, dir)
	if err != nil {
		return nil, err
	}
	identities := make([]Identity, 0, len(controllers))
	for _, controller := range controllers {
		identities = append(identities, Identity{PID: controller.PID, Started: controller.Start})
	}
	return identities, nil
}
