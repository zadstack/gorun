package edge

import "testing"

func TestSitePermissions(t *testing.T) {
 site:=Site{ID:"site-a",PrimaryManagerID:"owner",Grants:[]Grant{{ManagerID:"editor",Capabilities:[]Capability{ReadContent,EditContent}}}}
 cases:=[]struct{manager string; capability Capability; want bool}{
 {"owner",ManageManagers,true},
 {"editor",EditContent,true},
 {"editor",PublishContent,false},
 {"editor",ManageManagers,false},
 {"unknown",ReadContent,false},
 {"",ReadContent,false},
 {"owner",Capability("shell.exec"),false},
 }
 for _,tc:=range cases {
  if got:=site.Allowed(tc.manager,tc.capability);got!=tc.want {t.Errorf("%s %s: got %v want %v",tc.manager,tc.capability,got,tc.want)}
 }
}
func TestNoImplicitOwner(t *testing.T) {
 if (Site{}).Allowed("",ManageManagers) { t.Fatal("empty owner must not authorize") }
}
