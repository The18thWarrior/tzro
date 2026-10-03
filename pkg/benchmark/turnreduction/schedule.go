package turnreduction

type ScheduledCell struct {
	FixtureID string    `json:"fixture_id"`
	Condition Condition `json:"condition"`
}

// BalancedSchedule rotates positions within adjacent matched task groups.
func BalancedSchedule(fixtures []Fixture, conditions []Condition) []ScheduledCell {
	var cells []ScheduledCell
	for index, fixture := range fixtures {
		for position := range conditions {
			cells = append(cells, ScheduledCell{FixtureID: fixture.ID, Condition: conditions[(index+position)%len(conditions)]})
		}
	}
	return cells
}
