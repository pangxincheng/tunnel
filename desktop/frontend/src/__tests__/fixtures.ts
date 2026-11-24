import type { AppState, ServerView, DeviceView } from '../api'

export function device(over: Partial<DeviceView> = {}): DeviceView {
  return {
    device_id: 'id-' + (over.name ?? 'dev'),
    name: 'dev',
    hostname: 'dev.local',
    os: 'linux',
    os_version: '',
    arch: 'amd64',
    tunnel_version: '1.2.0',
    started_at: '',
    online: true,
    port: 5102,
    on: false,
    err: '',
    ...over,
  }
}

export function server(over: Partial<ServerView> = {}): ServerView {
  return {
    name: 'home',
    host: 'vps.example.com',
    user: 'tunnel',
    port: 22,
    key_path: '/home/u/.ssh/id_ed25519',
    host_key: 'ssh-ed25519 AAAAhome',
    socket_dir: '',
    conn: 'ready',
    conn_err: '',
    connected: 1,
    online: 3,
    share: {
      on: false,
      name: 'macbook',
      system: false,
      phase: 'off',
      err: '',
      name_err: '',
      busy: false,
      log_path: '',
      outdated: false,
    },
    devices: [],
    ...over,
  }
}

export function appState(over: Partial<AppState> = {}): AppState {
  return {
    servers: [],
    selected: '',
    settings: { mode: 'system', accent: 'green', lang: 'zh', launch_at_login: false, restore_forwards: true },
    platform: 'linux',
    sshd: { running: true, port: 22 },
    ...over,
  }
}
