package game

type EnemyProfile struct {
	HP          int
	AC          int
	AttackBonus int
	Damage      string
}

// Empty is the legacy profile so existing campaigns keep their combat values.
func NPCProfile(threat string) EnemyProfile {
	switch threat {
	case "minor":
		return EnemyProfile{HP: 8, AC: 10, AttackBonus: 1, Damage: "1d4"}
	case "elite":
		return EnemyProfile{HP: 24, AC: 14, AttackBonus: 4, Damage: "1d6+2"}
	default:
		return EnemyProfile{HP: 12, AC: 12, AttackBonus: 2, Damage: "1d4+1"}
	}
}
