import type { MsgKey } from './i18n'

/** The six connection commands for a forwarded port; text is not translated. */
export function commandsFor(port: number): { key: MsgKey; text: string }[] {
  return [
    { key: 'cmdSsh', text: `ssh -p ${port} <USERNAME>@127.0.0.1` },
    { key: 'cmdUpload', text: `scp -P ${port} <本地文件> <USERNAME>@127.0.0.1:<路径>` },
    { key: 'cmdDownload', text: `scp -P ${port} <USERNAME>@127.0.0.1:<路径> <本地目录>` },
    { key: 'cmdRsync', text: `rsync -av -e "ssh -p ${port}" <源> <USERNAME>@127.0.0.1:<目标>` },
    { key: 'cmdSftp', text: `sftp -P ${port} <USERNAME>@127.0.0.1` },
    { key: 'cmdAddr', text: `127.0.0.1:${port}` },
  ]
}
