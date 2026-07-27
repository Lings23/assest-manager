export type Role = 'reporter' | 'reviewer' | 'auditor' | 'admin'
export type DataScope = 'own' | 'department' | 'descendants' | 'global'

export interface Principal {
  user_id: string
  username: string
  display_name: string
  department_id?: string
  roles: Role[]
  data_scope: DataScope
  permissions_version: number
  must_change_password: boolean
}

export interface TokenResponse {
  access_token: string
  token_type: 'Bearer'
  expires_in: 900
  user: Principal
}
