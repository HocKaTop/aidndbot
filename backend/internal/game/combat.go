package game

type AttackResult struct {
	Target   string `json:"target"`
	Roll     Roll   `json:"roll"`
	Hit      bool   `json:"hit"`
	Damage   int    `json:"damage"`
	TargetHP int    `json:"targetHp"`
}

func Hits(natural, modifier, ac int) bool {
	return natural == 20 || (natural != 1 && natural+modifier >= ac)
}
func ApplyDamage(hp, damage int) int {
	if damage < 0 {
		return hp
	}
	if damage >= hp {
		return 0
	}
	return hp - damage
}
func Attack(target string, hp, ac, modifier int, damage string) (AttackResult, error) {
	r, e := RollDice("d20")
	out := AttackResult{Target: target, Roll: r, TargetHP: hp}
	if e != nil {
		return out, e
	}
	out.Hit = Hits(r.Total, modifier, ac)
	if out.Hit {
		d, e := RollDice(damage)
		if e != nil {
			return out, e
		}
		out.Damage = d.Total
		if r.Total == 20 {
			out.Damage *= 2
		}
		out.TargetHP = ApplyDamage(hp, out.Damage)
	}
	return out, nil
}
