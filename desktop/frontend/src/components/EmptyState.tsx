import { t } from '../i18n'

export default function EmptyState({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="empty">
      <h2>{t('emptyTitle')}</h2>
      <p>{t('emptyBody')}</p>
      <div className="empty-act">
        <button type="button" className="btn primary" onClick={onAdd}>
          {t('addServer')}
        </button>
      </div>
    </div>
  )
}
