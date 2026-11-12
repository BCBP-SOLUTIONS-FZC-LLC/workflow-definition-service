package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

func TestInternalEvents_EveryHandledTypeReachesACase(t *testing.T) {
	t.Parallel()

	for _, eventType := range handler.HandledEventTypes() {
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()
			rev := &fakeMembershipRevoker{}
			off := &fakeOffboarder{}

			w := w6aPostEvent(rev, off, w6aEventBody(t, eventType, ieTenantID.String(), w6aValidPayload()))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			reached := rev.called || rev.tenantCalled || len(off.recorded) > 0 || len(off.returned) > 0
			assert.True(t, reached, "%s is listed as handled but no case runs it: the event would be acknowledged and lost", eventType)
		})
	}
}

func TestInternalEvents_TenantIDInAnotherUUIDForm_SetsTheRowLevelSecurityTenantToTheParsedID(t *testing.T) {
	t.Parallel()

	forms := map[string]string{
		"urn form":        "urn:uuid:" + ieTenantID.String(),
		"braces":          "{" + ieTenantID.String() + "}",
		"no hyphens":      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"upper case":      "BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB",
		"canonical form":  ieTenantID.String(),
		"urn, upper case": "URN:UUID:BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB",
	}
	for name, tenant := range forms {
		for _, eventType := range handler.HandledEventTypes() {
			t.Run(name+"/"+eventType, func(t *testing.T) {
				t.Parallel()
				rev := &fakeMembershipRevoker{}
				off := &fakeOffboarder{}

				w := w6aPostEvent(rev, off, w6aEventBody(t, eventType, tenant, w6aValidPayload()))

				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				var got string
				switch {
				case rev.called || rev.tenantCalled:
					got = rev.gotGUC
				case len(off.gotGUC) == 1:
					got = off.gotGUC[0]
				default:
					t.Fatal("no service call reached")
				}
				assert.Equal(t, ieTenantID.String(), got, "the setting is the canonical text of the parsed tenant id, not the envelope string")
			})
		}
	}
}
