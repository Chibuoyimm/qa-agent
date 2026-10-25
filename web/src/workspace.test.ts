import test from 'node:test'
import assert from 'node:assert/strict'
import { coverageGroups, runCounts } from './workspace.ts'
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
