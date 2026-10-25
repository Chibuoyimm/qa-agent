export async function openStandaloneProposals(page) {
  await page.getByRole('navigation', {name:'Main navigation'}).getByRole('button', {name:'Settings',exact:true}).click();
  await page.getByRole('button', {name:'App & access',exact:true}).click();
  if (!await page.getByRole('button', {name:'Ask AI to propose',exact:true}).isVisible()) await page.getByText(/^Advanced check editor/).click();
  await page.getByRole('button', {name:'Ask AI to propose',exact:true}).click();
}
export async function openEarlierConversations(page) {
  await page.getByRole('navigation', {name:'Main navigation'}).getByRole('button', {name:'Settings',exact:true}).click();
  await page.getByRole('button', {name:'App & access',exact:true}).click();
  await page.getByRole('button', {name:'Open earlier conversations',exact:true}).click();
}
