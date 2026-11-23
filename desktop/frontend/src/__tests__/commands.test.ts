import { describe, it, expect } from 'vitest'
import { commandsFor } from '../commands'

describe('commandsFor', () => {
  it('returns the six commands verbatim, in order', () => {
    expect(commandsFor(5102).map((c) => c.text)).toEqual([
      'ssh -p 5102 <USERNAME>@127.0.0.1',
      'scp -P 5102 <本地文件> <USERNAME>@127.0.0.1:<路径>',
      'scp -P 5102 <USERNAME>@127.0.0.1:<路径> <本地目录>',
      'rsync -av -e "ssh -p 5102" <源> <USERNAME>@127.0.0.1:<目标>',
      'sftp -P 5102 <USERNAME>@127.0.0.1',
      '127.0.0.1:5102',
    ])
  })
})
