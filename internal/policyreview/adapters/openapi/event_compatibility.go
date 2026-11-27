package openapi

import "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"

type EventComparator struct{}

func (EventComparator) Tool() domain.Tool {
	return domain.Tool{Name: "fabric-asyncapi-compatibility", Version: "1"}
}

// Compare recognizes changes to named channels and operations in AsyncAPI
// 3.1.0. Message, server, binding and reference changes require additional
// compatibility evidence.
func (EventComparator) Compare(baseline, candidate domain.Source) domain.Comparison {
	diff := comparison{Classification: domain.Compatible}
	if baseline.Closure != domain.ClosureVerified || candidate.Closure != domain.ClosureVerified {
		diff.add(domain.Unknown, "REFERENCE_CLOSURE_UNVERIFIED", "")
		return diff.result()
	}
	before, after := comparableEventDocument(baseline), comparableEventDocument(candidate)
	if before == nil || after == nil {
		diff.add(domain.Unknown, "DOCUMENT_UNSUPPORTED", "")
		return diff.result()
	}
	if baseline.Entrypoint != candidate.Entrypoint || !sameCompanionFiles(baseline, candidate) {
		diff.add(domain.Unknown, "REFERENCE_FILES_CHANGED", "")
	}
	if !equalExcept(objectAt(before, "info"), objectAt(after, "info"), "title", "description", "version") ||
		!equalExcept(before, after, "info", "channels", "operations") {
		diff.add(domain.Unknown, "DOCUMENT_CHANGED", "")
	}
	diff.eventChannels(objectAt(before, "channels"), objectAt(after, "channels"))
	diff.eventOperations(objectAt(before, "operations"), objectAt(after, "operations"))
	return diff.result()
}

func comparableEventDocument(source domain.Source) object {
	parsed := read(source)
	if !parsed.readable() || len(parsed.eventSpecVersion().findings) > 0 {
		return nil
	}
	return validatedDocument(parsed.entrypoint(), eventSchema)
}

func (d *comparison) eventChannels(before, after object) {
	for _, key := range differentKeys(before, after) {
		location := "/channels/" + escapePointer.Replace(key)
		oldChannel, newChannel := objectAt(before, key), objectAt(after, key)
		switch {
		case newChannel == nil:
			d.add(domain.Breaking, "CHANNEL_REMOVED", location)
		case oldChannel == nil:
			d.add(domain.Unknown, "CHANNEL_ADDED", location)
		case oldChannel["address"] != nil && newChannel["address"] != nil &&
			changed(oldChannel, newChannel, "address"):
			d.add(domain.Breaking, "CHANNEL_ADDRESS_CHANGED", location+"/address")
		case !equalExcept(oldChannel, newChannel, "title", "summary", "description"):
			d.add(domain.Unknown, "CHANNEL_CHANGED", location)
		}
	}
}

func (d *comparison) eventOperations(before, after object) {
	for _, key := range differentKeys(before, after) {
		location := "/operations/" + escapePointer.Replace(key)
		oldOperation, newOperation := objectAt(before, key), objectAt(after, key)
		switch {
		case newOperation == nil:
			d.add(domain.Breaking, "OPERATION_REMOVED", location)
		case oldOperation == nil:
			d.add(domain.Unknown, "OPERATION_ADDED", location)
		case oldOperation["action"] != nil && newOperation["action"] != nil &&
			changed(oldOperation, newOperation, "action"):
			d.add(domain.Breaking, "OPERATION_DIRECTION_CHANGED", location+"/action")
		case !equalExcept(oldOperation, newOperation, "title", "summary", "description", "externalDocs"):
			d.add(domain.Unknown, "OPERATION_CHANGED", location)
		}
	}
}
