import type { ServerView } from '../api'

// Placeholder: Task 10 replaces the body (share card, device list, banners).
export default function ServerDetail({ server }: { server: ServerView }) {
  return (
    <>
      <div className="main-top">
        <span className="t">{server.name}</span>
        <span className="h" title={`${server.user}@${server.host}`}>
          {server.user}@{server.host}
        </span>
      </div>
      <div className="content" />
    </>
  )
}
