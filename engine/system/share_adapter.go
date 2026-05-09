package system

import (
	"context"

	"github.com/kuraos-org/kura/engine/share"
)

// ShareOwnershipAdapter wraps an Engine as a share.OwnershipApplier so
// engine/share can ask engine/system to chown/chmod the share path
// without importing this package directly.
type ShareOwnershipAdapter struct {
	eng Engine
}

func NewShareOwnershipAdapter(eng Engine) *ShareOwnershipAdapter {
	return &ShareOwnershipAdapter{eng: eng}
}

func (a *ShareOwnershipAdapter) ApplyOwnership(ctx context.Context, sh share.Share) error {
	target := ShareTarget{
		ID:   sh.ID,
		Name: sh.Name,
		Path: sh.Path,
	}
	for _, ace := range sh.ACL {
		if ace.Kind == share.PrincipalGroup && ace.Mode == share.ACLModeReadWrite {
			target.GroupNames = append(target.GroupNames, ace.Name)
		}
	}
	return a.eng.ApplyShareOwnership(ctx, target)
}
