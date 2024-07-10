import React, { useEffect, useState } from 'react'
import { Menu, MenuButton, MenuList, MenuItem, IconButton } from '@chakra-ui/react'
import { HiArrowRight, HiDotsVertical } from 'react-icons/hi'

function MenuOptions({ options = [], showDashboardOptions, ...rest }) {
  const [menuOptions, setMenuOptions] = useState([])

  useEffect(() => {
    if (showDashboardOptions) {
      setMenuOptions([
        {
          name: 'HA',
          subMenu: [
            { name: 'Reset Failover Counter' },
            { name: 'Rotate SLA' },
            { name: 'Toggle Traffic' },
            { name: 'Switchover' },
            { name: 'Failover' }
          ]
        },
        {
          name: 'Provision',
          subMenu: [{ name: 'New Monitor' }, { name: 'Provision Cluster' }, { name: 'Unprovision Cluster' }]
        },
        {
          name: 'Credentials',
          subMenu: [
            { name: 'Set Database Credentials' },
            { name: 'Set Replication Credentials' },
            { name: 'Set ProxySQL Credentials' },
            { name: 'Set Maxscale Credentials' },
            { name: 'Set Sharding Proxy Credentials' },
            { name: 'Rotate Database Credentials' }
          ]
        },
        {
          name: 'Maintenance',
          subMenu: [
            { name: 'Rolling Optimize' },
            { name: 'Rolling Restart' },
            { name: 'Rotate Certificates' },
            { name: 'Reload Certificates' },
            { name: 'Cancel Rolling Restart' },
            { name: 'Cancel Rolling Reprove' }
          ]
        },
        {
          name: 'Replication Bootstrap',
          subMenu: [
            { name: 'Master Slave' },
            { name: 'Master Slave Positional' },
            { name: 'Multi Master' },
            { name: 'Multi Master Ring' },
            { name: 'Multi Tier Slave' }
          ]
        },
        {
          name: 'Config',
          subMenu: [{ name: 'Reload' }, { name: 'Database discover config' }, { name: 'Database apply dynamic config' }]
        },
        {
          name: 'Debug',
          subMenu: [{ name: 'Clusters' }, { name: 'Servers' }, { name: 'Proxies' }]
        }
      ])
    }
  }, [showDashboardOptions])

  const styles = {
    menuButton: {
      borderRadius: '50%',
      width: '24px',
      height: '24px',
      minWidth: '24px',
      marginLeft: 'auto'
    }
  }
  return (
    <Menu colorScheme='blue' {...rest}>
      <MenuButton aria-label='Options' as={IconButton} sx={styles.menuButton} icon={<HiDotsVertical />}></MenuButton>
      <MenuList>
        {menuOptions?.map((option) => {
          return option.subMenu ? (
            <Menu>
              <MenuItem as={MenuButton} rightIcon={<HiArrowRight />}>
                {option.name}
              </MenuItem>
              <MenuList>
                {option.subMenu.map((subMenuOption) => (
                  <MenuItem>{subMenuOption.name}</MenuItem>
                ))}
              </MenuList>
            </Menu>
          ) : (
            <MenuItem>{option.name}</MenuItem>
          )
        })}
      </MenuList>
    </Menu>
  )
}

export default MenuOptions
