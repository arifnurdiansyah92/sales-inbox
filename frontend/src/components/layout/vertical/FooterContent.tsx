'use client'

// Third-party Imports
import classnames from 'classnames'

// Config Imports
import themeConfig from '@configs/themeConfig'

// Util Imports
import { verticalLayoutClasses } from '@layouts/utils/layoutClasses'

const FooterContent = () => {
  return (
    <div className={classnames(verticalLayoutClasses.footerContent, 'flex items-center justify-center flex-wrap gap-4')}>
      <p className='text-textSecondary'>{`© ${new Date().getFullYear()} ${themeConfig.templateName}`}</p>
    </div>
  )
}

export default FooterContent
