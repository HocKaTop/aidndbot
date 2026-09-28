package game

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
	"strconv"
)

// DiceNotationPattern is shared with structured AI output. Preserve the
// parser's padded forms (01d0006+003), as well as its numeric and width limits.
const DiceNotationPattern = `^(0?[1-9]|1[0-9]|20)?d(0{0,3}[2-9]|0{0,2}[1-9][0-9]|0?[1-9][0-9]{2}|1000)([+-](0{0,2}[0-9]|0?[1-9][0-9]|100))?$`

var notation = regexp.MustCompile(DiceNotationPattern)

type Dice struct {
	Count    int
	Sides    int
	Modifier int
}
type Roll struct {
	Notation string `json:"notation"`
	Rolls    []int  `json:"rolls"`
	Total    int    `json:"total"`
}

func ParseDice(s string) (Dice, error) {
	var d Dice
	m := notation.FindStringSubmatch(s)
	if m == nil {
		return d, errors.New("используй запись d20 или 2d6+3")
	}
	d.Count = 1
	if m[1] != "" {
		d.Count, _ = strconv.Atoi(m[1])
	}
	d.Sides, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		d.Modifier, _ = strconv.Atoi(m[3])
	}
	if d.Count < 1 || d.Count > 20 || d.Sides < 2 || d.Sides > 1000 || d.Modifier < -100 || d.Modifier > 100 {
		return d, errors.New("слишком большой бросок")
	}
	return d, nil
}
func RollDice(s string) (Roll, error) {
	r := Roll{Notation: s, Rolls: []int{}}
	d, e := ParseDice(s)
	if e != nil {
		return r, e
	}
	r.Total = d.Modifier
	for i := 0; i < d.Count; i++ {
		n, e := rand.Int(rand.Reader, big.NewInt(int64(d.Sides)))
		if e != nil {
			return r, e
		}
		v := int(n.Int64()) + 1
		r.Rolls = append(r.Rolls, v)
		r.Total += v
	}
	return r, nil
}
