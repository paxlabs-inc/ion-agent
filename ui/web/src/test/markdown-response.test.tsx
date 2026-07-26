import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MarkdownResponse } from '../features/chat/ChatHost'

const markdown = `# Useful answer

This has **strong text**, *emphasis*, ~~obsolete text~~, and \`inline code\`.

> A clear callout.

- First item
- [x] Completed task
- [ ] Remaining task

| Capability | Status |
| --- | --- |
| Markdown | Ready |

\`\`\`ts
const answer = 42
\`\`\`

[Official docs](https://example.com/docs)
`

describe('MarkdownResponse', () => {
  it('renders common and GitHub-flavored Markdown as semantic content', () => {
    const { container } = render(<MarkdownResponse content={markdown} />)

    expect(
      screen.getByRole('heading', { level: 1, name: 'Useful answer' }),
    ).toBeInTheDocument()
    expect(screen.getByText('strong text').tagName).toBe('STRONG')
    expect(screen.getByText('emphasis').tagName).toBe('EM')
    expect(screen.getByText('obsolete text').tagName).toBe('DEL')
    expect(screen.getByText('inline code').tagName).toBe('CODE')
    expect(screen.getByText('A clear callout.').closest('blockquote')).not.toBeNull()
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(screen.getAllByRole('checkbox')).toHaveLength(2)
    expect(screen.getAllByRole('checkbox')[0]).toBeChecked()
    expect(screen.getAllByRole('checkbox')[1]).not.toBeChecked()
    expect(container.querySelector('pre code.language-ts')).toHaveTextContent(
      'const answer = 42',
    )

    const link = screen.getByRole('link', { name: 'Official docs' })
    expect(link).toHaveAttribute('href', 'https://example.com/docs')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('does not execute raw HTML or unsafe link protocols', () => {
    const { container } = render(
      <MarkdownResponse
        content={'<script>alert("owned")</script>\n\n[unsafe](javascript:alert(1))'}
      />,
    )

    expect(container.querySelector('script')).toBeNull()
    const unsafeLink = container.querySelector('a')
    expect(unsafeLink).not.toBeNull()
    expect(unsafeLink?.getAttribute('href') ?? '').not.toMatch(/^javascript:/i)
  })
})
