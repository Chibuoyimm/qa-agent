import test from 'node:test'
import assert from 'node:assert/strict'
import { coverageGroups, runCounts, requiresPlanRevision, scenarioInput } from './workspace.ts'
import type { CoverageData, Run, Scenario } from './api.ts'
const scenario = (id: string, journey_id = ''): Scenario => ({ id, journey_id, project_id: 'app', name: id, description: '', expected_outcome: 'Independent expected outcome', approved: true, steps: [], created_at: '' })
test('coverage includes untested journeys, drafts without runs, and older ungrouped checks', () => {
 const check = scenario('existing','checkout')
 const data: CoverageData = { journeys: [{id:'checkout',name:'Checkout',project_id:'app',created_at:''},{id:'login',name:'Sign in',project_id:'app',created_at:''}], scenarios: [check,scenario('draft','login'),scenario('legacy')], checks:[{scenario:check,result:{scenario_id:'existing',status:'passed',message:'',duration_ms:1,artifacts:[]},run_id:'latest',base_url:'https://preview.example',finished_at:''}] }
 const groups = coverageGroups(data)
 assert.equal(groups.find(group => group.id === 'checkout')?.executed,1)
 assert.equal(groups.find(group => group.id === 'login')?.executed,0)
 assert.equal(groups.find(group => group.id === '')?.checks[0].scenario.id,'legacy')
 // An unsuccessful latest attempt replaces a previous proof; it cannot stay green.
 data.checks[0].result.status='blocked'
 assert.equal(coverageGroups(data).find(group => group.id === 'checkout')?.executed,0)
})
test('cancelled/partial runs identify unfinished checks separately from assertion results', () => {
 const run={scenarios:[scenario('a'),scenario('b'),scenario('c')],results:[{scenario_id:'a',status:'passed'},{scenario_id:'b',status:'error'}]} as Run
 assert.deepEqual(runCounts(run),{passed:1,failed:0,blocked:0,error:1,incomplete:1})
})

test('changed prose requires revised assertions, not just altered navigation', async () => {
 const {requiresPlanRevision}=await import('./workspace.ts')
 const original={...scenario('button'),expected_outcome:'Button says Sign in',steps:[{action:'navigate',path:'/'},{action:'assert_text',test_id:'submit',value:'Sign in'}]} as Scenario
 const draft={...original,expected_outcome:'Button says Continue'}
 assert.equal(requiresPlanRevision(original,draft),true)
 assert.equal(requiresPlanRevision(original,{...draft,steps:[{action:'navigate',path:'/login'},original.steps[1]]}),true)
 assert.equal(requiresPlanRevision(original,{...draft,steps:[original.steps[0],{...original.steps[1],value:'Continue'}]}),false)
})
test('comparison distinguishes fixed, new failures, still failing and missing proof', async () => {
 const {compareRuns,groupRuns}=await import('./workspace.ts')
 const before={id:'one',created_at:'1',scenarios:['a','b','c','d'].map(id=>scenario(id)),results:[{scenario_id:'a',status:'failed'},{scenario_id:'b',status:'passed'},{scenario_id:'c',status:'failed'},{scenario_id:'d',status:'failed'}]} as Run
 const after={...before,id:'two',source_run_id:'one',created_at:'2',results:[{scenario_id:'a',status:'passed'},{scenario_id:'b',status:'failed'},{scenario_id:'c',status:'failed'}]} as Run
 assert.deepEqual(compareRuns(before,after).map(r=>r.changed),['Fixed','New failure','Still failing','Unverified'])
 const third={...after,id:'three',source_run_id:'two',created_at:'3'}
 assert.equal(groupRuns([third,after,before]).length,1)
 assert.equal(groupRuns([third,after,before])[0].length,3)
})

test('accessible assertions retain their target through validation and revision review', async () => {
 const {validateScenario}=await import('./api.ts')
 const original={...scenario('accessible'),expected_outcome:'Denied',steps:[{action:'assert_visible',locator:{role:'heading',name:'Denied'}}]} as Scenario
 const changed={...original,expected_outcome:'Allowed',steps:[{action:'assert_visible',locator:{role:'heading',name:'Allowed'}}]} as Scenario
 assert.equal(requiresPlanRevision(original, {...original,expected_outcome:'Allowed'}),true)
 assert.equal(requiresPlanRevision(original, changed),false)
 const validated = validateScenario({...scenarioInput(changed),steps:[{action:'fill',locator:{label:'Password'},secret_env:'QA_TEST_PASSWORD'},...changed.steps]})
 assert.deepEqual(validated.steps[0].locator,{label:'Password'})
 assert.throws(()=>validateScenario({...scenarioInput(changed),steps:[{action:'assert_visible',locator:{role:'heading',name:'Allowed'},test_id:'other'}]}))
})
