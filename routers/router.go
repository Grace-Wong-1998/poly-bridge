/*
 * Copyright (C) 2020 The poly network Authors
 * This file is part of The poly network library.
 *
 * The  poly network  is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The  poly network  is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 * You should have received a copy of the GNU Lesser General Public License
 * along with The poly network .  If not, see <http://www.gnu.org/licenses/>.
 */

package routers

import (
	"github.com/astaxie/beego"
	"poly-swap/controllers"
)

func init() {
	ns := beego.NewNamespace("/v1",
		beego.NSRouter("/", &controllers.InfoController{}, "*:Get"),
		beego.NSRouter("/token/", &controllers.TokenController{}, "post:Token"),
		beego.NSRouter("/tokens/", &controllers.TokenController{}, "post:Tokens"),
		beego.NSRouter("/tokenbasics/", &controllers.TokenController{}, "post:TokenBasic"),
		beego.NSRouter("/tokenmap/", &controllers.TokenMapController{}, "post:TokenMap"),
		beego.NSRouter("/getfee/", &controllers.FeeController{}, "post:GetFee"),
		beego.NSRouter("/checkfee/", &controllers.FeeController{}, "post:CheckFee"),
		beego.NSRouter("/transactoins/", &controllers.TransactionController{}, "post:Transactions"),
		beego.NSRouter("/transactoinsofuser/", &controllers.TransactionController{}, "post:TransactoinsOfUser"),
		beego.NSRouter("/transactoinsofstate/", &controllers.TransactionController{}, "post:TransactoinsOfState"),
	)
	beego.AddNamespace(ns)
	beego.Router("/", &controllers.InfoController{}, "*:Get")
}
