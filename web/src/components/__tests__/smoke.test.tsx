import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'

test('test framework renders React components', () => {
  render(<div data-testid="smoke">fleet-llm-d</div>)
  expect(screen.getByTestId('smoke')).toBeDefined()
  expect(screen.getByTestId('smoke').textContent).toBe('fleet-llm-d')
})
