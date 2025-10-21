package affliction

import (
	"time"

	"github.com/wowsims/mop/sim/core"
	"github.com/wowsims/mop/sim/core/proto"
)

func (warlock *AfflictionWarlock) NewAPLValue(rot *core.APLRotation, config *proto.APLValue) core.APLValue {
	switch config.Value.(type) {
	case *proto.APLValue_WarlockHauntInFlight:
		spellInFlight := proto.APLValueSpellInFlight{
			SpellId: core.Spell{ActionID: core.ActionID{SpellID: 48181}}.ToProto(),
		}
		return rot.NewValueSpellInFlight(&spellInFlight, nil)
	case *proto.APLValue_AfflictionHauntDamageEstimate:
		return warlock.newValueHauntDamageEstimate(config.GetAfflictionHauntDamageEstimate(), config.Uuid)
	case *proto.APLValue_AfflictionSbssDamageEstimate:
		return warlock.newValueSBSSDamageEstimate(config.GetAfflictionSbssDamageEstimate(), config.Uuid)
	case *proto.APLValue_AfflictionDsDamageCost:
		return warlock.newValueDSDamageCost(config.GetAfflictionDsDamageCost(), config.Uuid)
	default:
		return warlock.Warlock.NewAPLValue(rot, config)
	}
}

func (warlock *AfflictionWarlock) NewAPLAction(rot *core.APLRotation, config *proto.APLAction) core.APLActionImpl {
	switch config.Action.(type) {
	case *proto.APLAction_WarlockNextExhaleTarget:
		return warlock.newActionNextExhaleTarget(config.GetWarlockNextExhaleTarget())
	default:
		return nil
	}
}

type APLActionNextExhaleTarget struct {
	warlock        *AfflictionWarlock
	lastExecutedAt time.Duration
}

// Execute implements core.APLActionImpl.
func (action *APLActionNextExhaleTarget) Execute(sim *core.Simulation) {
	action.lastExecutedAt = sim.CurrentTime
	if action.warlock.CurrentTarget != action.warlock.LastInhaleTarget {
		return
	}

	nextTarget := core.NewUnitReference(&proto.UnitReference{Type: proto.UnitReference_NextTarget}, &action.warlock.Unit).Get()
	if nextTarget == nil {
		return
	}

	if sim.Log != nil {
		action.warlock.Log(sim, "Changing target to %s", nextTarget.Label)
	}

	action.warlock.CurrentTarget = nextTarget
}

func (action *APLActionNextExhaleTarget) Finalize(*core.APLRotation)         {}
func (action *APLActionNextExhaleTarget) GetAPLValues() []core.APLValue      { return nil }
func (action *APLActionNextExhaleTarget) GetInnerActions() []*core.APLAction { return nil }
func (action *APLActionNextExhaleTarget) GetNextAction(sim *core.Simulation) *core.APLAction {
	return nil
}
func (action *APLActionNextExhaleTarget) PostFinalize(*core.APLRotation) {}
func (action *APLActionNextExhaleTarget) ReResolveVariableRefs(*core.APLRotation, map[string]*proto.APLValue) {
}

func (action *APLActionNextExhaleTarget) IsReady(sim *core.Simulation) bool {
	// Prevent infinite loops by only allowing this action to be performed once at each timestamp.
	return action.lastExecutedAt != sim.CurrentTime
}

// Reset implements core.APLActionImpl.
func (action *APLActionNextExhaleTarget) Reset(sim *core.Simulation) {
	action.lastExecutedAt = core.NeverExpires
}

// String implements core.APLActionImpl.
func (action *APLActionNextExhaleTarget) String() string {
	return "Changing to Next Exhale Target"
}

func (warlock *AfflictionWarlock) newActionNextExhaleTarget(config *proto.APLActionWarlockNextExhaleTarget) core.APLActionImpl {
	return &APLActionNextExhaleTarget{
		warlock:        warlock,
		lastExecutedAt: core.NeverExpires,
	}
}

// calculates the total damage value of haunt (base damage + 35% damage on 8 secs of current dots (ignoring mg))
type APLValueAfflictionHauntDamageEstimate struct {
	core.DefaultAPLValueImpl
	warlock             *AfflictionWarlock
	hauntDamageEstimate float64
}

func (warlock *AfflictionWarlock) newValueHauntDamageEstimate(_ *proto.APLValueAfflictionHauntDamageEstimate, _ *proto.UUID) core.APLValue {
	if warlock.Spec != proto.Spec_SpecAfflictionWarlock {
		return nil
	}

	return &APLValueAfflictionHauntDamageEstimate{
		warlock: warlock,
	}
}

func (value *APLValueAfflictionHauntDamageEstimate) Type() proto.APLValueType {
	return proto.APLValueType_ValueTypeFloat
}

func (value *APLValueAfflictionHauntDamageEstimate) GetFloat(sim *core.Simulation) float64 {
	target := value.warlock.CurrentTarget
	initialDamage := value.warlock.GetSpell(core.ActionID{SpellID: 48181}).ExpectedInitialDamage(sim, target)
	existingDmg := 0.0
	dots := [3]*core.Spell{
		value.warlock.GetSpell(core.ActionID{SpellID: 980}),   //agony
		value.warlock.GetSpell(core.ActionID{SpellID: 30108}), //UA
		value.warlock.GetSpell(core.ActionID{SpellID: 172}),   //corruption
	}
	for _, dot := range dots {
		if dot.Dot(target).IsActive() {
			existingDmg += dot.ExpectedTickDamageFromCurrentSnapshot(sim, target) * min(8, dot.Dot(target).RemainingDuration(sim).Seconds())
			if dot.Dot(target).RemainingDuration(sim).Seconds() < 8 {
				existingDmg += dot.ExpectedTickDamage(sim, target) * (8 - dot.Dot(target).RemainingDuration(sim).Seconds())
			}
		} else {
			existingDmg += dot.ExpectedTickDamage(sim, target) * 8
		}
	}

	value.hauntDamageEstimate = initialDamage + 0.35*existingDmg
	if sim.Log != nil {
		value.warlock.Log(sim, "xZ haunt dmg calculated as: %.f", value.hauntDamageEstimate)
	}
	return value.hauntDamageEstimate
}

func (value *APLValueAfflictionHauntDamageEstimate) String() string {
	return "Haunt Damage Estimated Value"
}

// calculates the damage difference between remaining damage on current dots and the total damage done by dots that wouljd be applied via SBSS
type APLValueAfflictionSBSSDamageEstimate struct {
	core.DefaultAPLValueImpl
	warlock            *AfflictionWarlock
	sbssDamageEstimate float64
}

func (warlock *AfflictionWarlock) newValueSBSSDamageEstimate(_ *proto.APLValueAfflictionSBSSDamageEstimate, _ *proto.UUID) core.APLValue {
	if warlock.Spec != proto.Spec_SpecAfflictionWarlock {
		return nil
	}

	return &APLValueAfflictionSBSSDamageEstimate{
		warlock: warlock,
	}
}

func (value *APLValueAfflictionSBSSDamageEstimate) Type() proto.APLValueType {
	return proto.APLValueType_ValueTypeFloat
}

func (value *APLValueAfflictionSBSSDamageEstimate) GetFloat(sim *core.Simulation) float64 {
	target := value.warlock.CurrentTarget
	newDmg := 0.0
	existingDmg := 0.0
	dots := [3]*core.Spell{
		value.warlock.GetSpell(core.ActionID{SpellID: 980}),   //agony
		value.warlock.GetSpell(core.ActionID{SpellID: 30108}), //UA
		value.warlock.GetSpell(core.ActionID{SpellID: 172}),   //corruption
	}
	for _, dot := range dots {
		newDmg += dot.ExpectedTickDamage(sim, target) * (dot.Dot(target).BaseDuration().Seconds() + min(dot.Dot(target).BaseDuration().Seconds()/2, dot.Dot(target).RemainingDuration(sim).Seconds()))
		if dot.Dot(target).IsActive() {
			existingDmg += dot.Dot(target).RemainingDuration(sim).Seconds() * dot.ExpectedTickDamageFromCurrentSnapshot(sim, target)
		}
	}

	value.sbssDamageEstimate = newDmg - existingDmg
	if sim.Log != nil {
		value.warlock.Log(sim, "xZ sbss dmg calculated as: %.f", value.sbssDamageEstimate)
	}
	return value.sbssDamageEstimate
}

func (value *APLValueAfflictionSBSSDamageEstimate) String() string {
	return "SBSS Estimated Value"
}

// Calculates the damage lost by drain souling for a shard as compared to malefic grasping.
// Does not take into account shard damage gain.
// Simply works on logic that 2 DS ticks = 4 MG ticks (in terms of time).
// Therefore is calculated as: (4 mg tick dmg + 2x each dots expected tick dmg) - (2 drain soul tick dmg)
type APLValueAfflictionDSDamageCost struct {
	core.DefaultAPLValueImpl
	warlock      *AfflictionWarlock
	dsDamageCost float64
}

func (warlock *AfflictionWarlock) newValueDSDamageCost(_ *proto.APLValueAfflictionDSDamageCost, _ *proto.UUID) core.APLValue {
	if warlock.Spec != proto.Spec_SpecAfflictionWarlock {
		return nil
	}

	return &APLValueAfflictionDSDamageCost{
		warlock: warlock,
	}
}

func (value *APLValueAfflictionDSDamageCost) Type() proto.APLValueType {
	return proto.APLValueType_ValueTypeFloat
}

func (value *APLValueAfflictionDSDamageCost) GetFloat(sim *core.Simulation) float64 {
	target := value.warlock.CurrentTarget
	mgDmg := 0.0
	dsDmg := 0.0

	dots := [3]*core.Spell{
		value.warlock.GetSpell(core.ActionID{SpellID: 980}),   //agony
		value.warlock.GetSpell(core.ActionID{SpellID: 30108}), //UA
		value.warlock.GetSpell(core.ActionID{SpellID: 172}),   //corruption
	}
	for _, dot := range dots {
		if dot.Dot(target).IsActive() {
			mgDmg += 2 * dot.ExpectedTickDamageFromCurrentSnapshot(sim, target) * dot.Dot(target).TickPeriod().Seconds()
		} else {
			mgDmg += 2 * dot.ExpectedTickDamage(sim, target) * dot.Dot(target).CalcTickPeriod().Seconds()
		}
	}
	mg := value.warlock.GetSpell(core.ActionID{SpellID: 103103})
	mgDmg += mg.ExpectedTickDamage(sim, target) * mg.Dot(target).CalcTickPeriod().Seconds() * 4

	ds := value.warlock.GetSpell(core.ActionID{SpellID: 1120})
	dsDmg += ds.ExpectedTickDamage(sim, target) * ds.Dot(target).CalcTickPeriod().Seconds() * 2

	value.dsDamageCost = mgDmg - dsDmg
	if sim.Log != nil {
		value.warlock.Log(sim, "xZ DS cost calculated as: %.f", value.dsDamageCost)
	}
	return value.dsDamageCost
}

func (value *APLValueAfflictionDSDamageCost) String() string {
	return "DS Estimated Damage Cost"
}
