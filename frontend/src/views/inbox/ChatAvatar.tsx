// Component Imports
import CustomAvatar from '@core/components/mui/Avatar'

// Util Imports
import { avatarUrl } from '@/utils/whatsappApi'
import { getInitials } from '@/utils/getInitials'

type Props = {
  jid: string
  name: string
  size?: number
}

const ChatAvatar = ({ jid, name, size = 40 }: Props) => (
  <CustomAvatar src={avatarUrl(jid)} alt={name} size={size} skin='light' color='primary'>
    {getInitials(name || jid.split('@')[0]).slice(0, 2)}
  </CustomAvatar>
)

export default ChatAvatar
