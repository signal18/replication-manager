import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { DataTable } from '../../components/DataTable'
import AccordionComponent from '../../components/AccordionComponent'
import styles from './styles.module.scss'
import UserGrantModal from '../../components/Modals/UserGrantModal'
import { acceptExternalRole, acceptSubscription, dropUser, endSubscription, refuseExternalRole, rejectSubscription, sendCredentials } from '../../redux/clusterSlice'
import RMButton from '../../components/RMButton'
import RMIconButton from '../../components/RMIconButton'
import { FaUserPlus } from 'react-icons/fa'
import AddUserModal from '../../components/Modals/AddUserModal'
import { HiUserGroup } from 'react-icons/hi'
import { TbDatabaseStar, TbDatabaseX, TbDeviceDesktopCancel, TbDeviceDesktopStar, TbDeviceDesktopX, TbDevicesStar, TbMail, TbMailCog, TbMailDollar, TbMailStar, TbTrash, TbUserCancel, TbUserStar } from 'react-icons/tb'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { useDispatch, useSelector } from 'react-redux'
import { HStack } from '@chakra-ui/react'
import TextInputModal from '../../components/Modals/TextInputModal'

function Users({ selectedCluster, user }) {
  const [isAddUserModalOpen, setIsAddUserModalOpen] = useState(false)
  const [data, setData] = useState([])
  const [selectedUser, setSelectedUser] = useState(null)
  const [action, setAction] = useState({ type: '', title: '', payload: '' })
  const [isUserGrantModalOpen, setIsUserGrantModalOpen] = useState(null)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(null)
  const [isTextModalOpen, setIsTextModalOpen] = useState(null)
  const columnHelper = createColumnHelper()
  const { type, title, payload } = action
  const dispatch = useDispatch()
  const {
    globalClusters: { monitor },
  } = useSelector((state) => state)
  const canAddUser = !!(monitor?.config?.monitoringSaveConfig && monitor?.config?.cloud18GitUser?.length > 0)

  const showUser = (user, item) => {
    if (user.user === "admin") {
      return true
    } else if (user.user === item.user) {
      return true
    } else if (user.roles['sysops']) {
      return true
    } else if (user.roles['dbops']) {
      return item.roles['extdbops']
    } else if (user.roles['sponsor']) {
      return item.roles['extdbops'] || item.roles['extsysops'] || item.roles['visitor']
    }
    return false
  }

  const isShowDropUser = (user, item) => {
    // Prevent dropping self, cloud18 sysops user, admin user, sponsor user, external dbops user, external sysops user
    let immutable = user.user == item.user || monitor?.config?.cloud18GitUser == item.user || item.user == "admin" || item.roles['sponsor'] || item.roles['extdbops'] || item.roles['extsysops']
    
    if (user.roles['sysops']) {
      return !immutable
    } else if (user.roles['sponsor']) {
      return item.roles['visitor']
    }
    return false
  }

  const isShowSendDB = (user, item) => {
    return user.roles['dbops'] || item.roles['extdbops']
  }

  const isShowSendSys = (user, item) => {
    return user.roles['sysops'] || item.roles['extsysops']
  }

  useEffect(() => {
    if (selectedCluster?.apiUsers) {
      const result = Object.entries(selectedCluster?.apiUsers).filter(([_, value]) => showUser(user, value)).map(([key, value]) => ({
        user: key,
        ...value
      }));

      setData(result)
    }
  }, [selectedCluster?.apiUsers])

  const openUserGrantModal = () => {
    setIsUserGrantModalOpen(true)
  }

  const closeUserGrantModal = () => {
    setIsUserGrantModalOpen(false)
  }

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const openTextModal = () => {
    setIsTextModalOpen(true)
  }

  const handleConfirm = (value) => {
    if (type === 'accept-sub') {
      dispatch(acceptSubscription({ clusterName: selectedCluster.name, username: payload }))
    } else if (type === 'reject-sub') {
      dispatch(rejectSubscription({ clusterName: selectedCluster.name, username: payload }))
    } else if (type === 'accept-extdbops') {
      dispatch(acceptExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops' }))
    } else if (type === 'reject-extdbops') {
      dispatch(refuseExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extdbops', reason: value }))
    } else if (type === 'accept-extsysops') {
      dispatch(acceptExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops' }))
    } else if (type === 'reject-extsysops') {
      dispatch(refuseExternalRole({ clusterName: selectedCluster.name, username: payload, roles: 'extsysops', reason: value }))
    } else if (type === 'end-sub') {
      dispatch(endSubscription({ clusterName: selectedCluster.name, username: payload }))
    } else if (type === 'drop-user') {
      dispatch(dropUser({ clusterName: selectedCluster.name, username: payload }))
    } else if (type === 'send-cred-sponsor') {
      dispatch(sendCredentials({ clusterName: selectedCluster.name, username: payload, type: 'sponsor' }))
    } else if (type === 'send-cred-db') {
      dispatch(sendCredentials({ clusterName: selectedCluster.name, username: payload, type: 'db' }))
    } else if (type === 'send-cred-sys') {
      dispatch(sendCredentials({ clusterName: selectedCluster.name, username: payload, type: 'sys' }))
    }
    closeConfirmModal()
  }

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.user, {
        cell: (info) => info.getValue(),
        header: 'User',
        id: 'user'
      }),
      columnHelper.accessor((row) => row.isExternal, {
        cell: (info) => info.getValue(),
        header: 'Is Git Validated',
        id: 'isExternal'
      }),
      columnHelper.accessor((row) => {
        return Object.entries(row.roles).filter(([_, v]) => v).map(([role, _]) => (<span>{role}</span>)).reduce((r, n) => {
          if (r.length) {
            r.push(<br />)
          }
          r.push(n)
          return r
        }, [])
      }, {
        cell: (info) => info.getValue(),
        header: 'Roles',
        id: 'roles'
      }),
      columnHelper.accessor((row) => (
        <HStack align={"center"} justifyContent={"center"}>
          { row?.roles?.["pending"] ? (
            <>
              <RMIconButton tooltip={"accept subscription"} icon={TbUserStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "accept-sub", title: "Are you sure to accept subscription?", payload: row.user }); openConfirmModal() }} />
              <RMIconButton tooltip={"reject subscription"} icon={TbUserCancel} onClick={(e) => { e.stopPropagation(); setAction({ type: "reject-sub", title: "Are you sure to reject subscription?", payload: row.user }); openConfirmModal() }} />
            </>
          ) : row?.roles?.["pending-extdbops"] ? (
            <>
              <RMIconButton tooltip={"accept external dbops"} icon={TbDatabaseStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "accept-extdbops", title: "Are you sure to accept external dbops?", payload: row.user }); openConfirmModal() }} />
              <RMIconButton tooltip={"reject external dbops"} icon={TbDatabaseX} onClick={(e) => { e.stopPropagation(); setAction({ type: "reject-extdbops", title: "Are you sure to reject external dbops?", payload: row.user }); openTextModal() }} />
            </>
          ) : row?.roles?.["pending-extsysops"] ? (
            <>
              <RMIconButton tooltip={"accept external sysops"} icon={TbDeviceDesktopStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "accept-extsysops", title: "Are you sure to accept external sysops?", payload: row.user }); openConfirmModal() }} />
              <RMIconButton tooltip={"reject external sysops"} icon={TbDeviceDesktopX} onClick={(e) => { e.stopPropagation(); setAction({ type: "reject-extsysops", title: "Are you sure to reject external sysops?", payload: row.user }); openTextModal() }} />
            </>
          ) : (
            <>
              { row?.roles?.["sponsor"] && <RMIconButton tooltip={"unsubscribe sponsorship"} icon={TbUserCancel} onClick={(e) => { e.stopPropagation(); setAction({ type: "end-sub", title: "Are you sure to end subscription?", payload: row.user }); openConfirmModal() }} />}
              { user?.user != row?.user && <RMIconButton tooltip={"user privileges"} icon={HiUserGroup} onClick={(e) => { e.stopPropagation(); setSelectedUser(row); openUserGrantModal() }} />}
              { row?.roles?.["sponsor"] && <RMIconButton tooltip={"send sponsor credentials"} icon={TbMailStar} onClick={(e) => { e.stopPropagation(); setAction({ type: "send-cred-sponsor", title: "Are you sure to send sponsor credentials to "+row.user+"?", payload: row.user }); openConfirmModal() }} />}
              { isShowSendDB(user, row) && <RMIconButton tooltip={"send dba credentials"} icon={TbMail} onClick={(e) => { e.stopPropagation(); setAction({ type: "send-cred-db", title: "Are you sure to send dba credentials to "+row.user+"?", payload: row.user }); openConfirmModal() }} />}
              { isShowSendSys(user,row) && <RMIconButton tooltip={"send sysadmin credentials"} icon={TbMailCog} onClick={(e) => { e.stopPropagation(); setAction({ type: "send-cred-sys", title: "Are you sure to send sys admin credentials to "+row.user+"?", payload: row.user }); openConfirmModal() }} />}
              { isShowDropUser(user,row) && <RMIconButton tooltip={"drop user"} icon={TbTrash} onClick={(e) => { e.stopPropagation(); setAction({ type: "drop-user", title: "Are you sure to drop user "+row.user+"?", payload: row.user }); openConfirmModal() }} /> }
            </>
          )}
        </HStack>
      ), {
        cell: (info) => info.getValue(),
        header: 'Actions',
        id: 'actions'
      })
    ],
    []
  )


  return (
    <>
      <AccordionComponent
        heading={'USERS'}
        allowToggle={false}
        className={styles.accordion}
        panelSX={{ overflowX: 'auto', p: 0 }}
        headerActions={
          canAddUser ? (
            <RMIconButton
              icon={FaUserPlus}
              tooltip={'Add User'}
              px='2'
              variant='outline'
              onClick={() => setIsAddUserModalOpen(true)}
            />
          ) : undefined
        }
        body={<DataTable key="users" data={data} columns={columns} className={styles.table} />}
      />
      {isAddUserModalOpen && (
        <AddUserModal
          clusterName={selectedCluster?.name}
          apiUsers={selectedCluster?.apiUsers}
          isOpen={isAddUserModalOpen}
          closeModal={() => setIsAddUserModalOpen(false)}
        />
      )}
      {isUserGrantModalOpen && <UserGrantModal clusterName={selectedCluster.name} selectedUser={selectedUser} isOpen={isUserGrantModalOpen} closeModal={closeUserGrantModal} />}
      {isConfirmModalOpen && <ConfirmModal title={title} isOpen={isConfirmModalOpen} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
      {isTextModalOpen && <TextInputModal isOpen={isTextModalOpen} title={title} fieldname={'Reason'} onSave={handleConfirm} isRequired={true} closeModal={() => { setIsTextModalOpen(false);}} />}
    </>
  )
}

export default Users
