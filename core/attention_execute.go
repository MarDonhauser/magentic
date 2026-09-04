package core

// AttentionExecutor führt die Absichten eines AttentionPlan aus. Er entscheidet
// nichts: Was unterdrückt wird, hat der Planner bereits entschieden und als
// AttentionSuppression verbucht. Ein nicht gesetzter Aufruf bedeutet, dass die
// Oberfläche diese Absicht nicht bedienen kann — nicht, dass sie sie ablehnt.
type AttentionExecutor struct {
	Badge   func(label string)
	Notify  func(title, message, sound string)
	Request func(critical bool)
	Cancel  func()
	Front   func()
}

// ExecuteAttentionPlan führt einen Plan über einen Executor aus. Beide
// Oberflächen — Desktop und TUI — gehen hier durch, damit dieselbe Policy
// dieselbe Wirkung hat.
func ExecuteAttentionPlan(plan AttentionPlan, executor AttentionExecutor) {
	if plan.DockBadge.Update && executor.Badge != nil {
		executor.Badge(plan.DockBadge.Label)
	}
	for _, notification := range plan.Notifications {
		if executor.Notify != nil {
			executor.Notify(notification.Title, notification.Message, notification.Sound)
		}
	}
	switch plan.NativeAttention {
	case NativeAttentionCancel:
		if executor.Cancel != nil {
			executor.Cancel()
		}
	case NativeAttentionInformational:
		if executor.Request != nil {
			executor.Request(false)
		}
	case NativeAttentionCritical:
		if executor.Request != nil {
			executor.Request(true)
		}
	}
	if plan.BringToFront && executor.Front != nil {
		executor.Front()
	}
}
