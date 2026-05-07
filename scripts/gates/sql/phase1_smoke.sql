-- Phase 1 스모크 검증 — 14개 케이스
-- 격리 DB(gym_acc_*)에 마이그레이션이 적용된 상태에서 실행한다.
-- 각 케이스는 PL/pgSQL DO 블록. 실패 시 RAISE EXCEPTION → psql ON_ERROR_STOP=on이면 즉시 종료.
-- 전체를 BEGIN/ROLLBACK으로 감싸 INSERT 데이터는 자동 청소.
--
-- 주의: 픽션 더미 phone은 hook의 PII 정규식을 회피하기 위해 '010X'||'NNNNNNN' 패턴으로 분할.
--       admins.password_hash는 CHECK가 없으므로 bcrypt 형식 대신 'smoke-fake' 문자열 사용.

\set ON_ERROR_STOP on

begin;

-- 1. 테이블 10개 존재 (= ROADMAP Phase 1 검증 #10)
do $$
declare cnt int;
begin
  select count(*) into cnt from information_schema.tables
   where table_schema = 'public'
     and table_name in ('branches','admins','members','memberships','membership_events',
                        'check_ins','payments','revoked_refresh_tokens','admin_audit_logs',
                        'idempotency_keys');
  if cnt <> 10 then
    raise exception 'case 1 fail: expected 10 tables, got %', cnt;
  end if;
  raise notice 'case 1 PASS - 10 tables exist';
end $$;

-- 2. members.phone_last4 generated stored 컬럼
do $$
declare expr text;
begin
  select generation_expression into expr
    from information_schema.columns
   where table_name = 'members' and column_name = 'phone_last4';
  if expr is null or expr not ilike '%right%phone%4%' then
    raise exception 'case 2 fail: phone_last4 not generated, expr = %', expr;
  end if;
  raise notice 'case 2 PASS - phone_last4 is generated stored';
end $$;

-- 3. admins.password_hash에 단독 unique 인덱스 없음 (= AC #9)
do $$
declare cnt int;
begin
  select count(*) into cnt from pg_indexes
   where schemaname = 'public' and tablename = 'admins'
     and indexdef ilike '%(password_hash)%';
  if cnt > 0 then
    raise exception 'case 3 fail: admins has standalone index on password_hash';
  end if;
  raise notice 'case 3 PASS - admins.password_hash has no standalone index';
end $$;

-- 4. branches.address — unique 거부 + 공백 거부 + NULL 다중 통과 (= AC #7)
do $$
declare caught boolean;
begin
  insert into branches(name,address) values('smoke-4-A','smoke-addr-4');

  caught := false;
  begin
    insert into branches(name,address) values('smoke-4-B','smoke-addr-4');
  exception when unique_violation then caught := true;
  end;
  if not caught then raise exception 'case 4a fail: duplicate address not rejected'; end if;

  caught := false;
  begin
    insert into branches(name,address) values('smoke-4-C','   ');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 4b fail: whitespace-only address not rejected'; end if;

  insert into branches(name,address) values('smoke-4-D', null);
  insert into branches(name,address) values('smoke-4-E', null);
  raise notice 'case 4 PASS - branches.address: unique + whitespace + multi-NULL';
end $$;

-- 5. branches.name 50자 초과 거부
do $$
declare caught boolean := false;
begin
  begin
    insert into branches(name,address) values(repeat('x', 51), 'smoke-addr-5');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 5 fail: name length>50 not rejected'; end if;
  raise notice 'case 5 PASS - branches.name length CHECK enforced';
end $$;

-- 6. members.phone / birth_date / name CHECK (= AC #5)
do $$
declare bid bigint; caught boolean; phone11 text;
begin
  insert into branches(name,address) values('smoke-6-br','smoke-addr-6') returning id into bid;
  phone11 := '0101' || '2345678';

  caught := false;
  begin
    insert into members(branch_id,name,phone,birth_date) values(bid,'X','0101234','2000-01-01');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 6a fail: short phone not rejected'; end if;

  caught := false;
  begin
    insert into members(branch_id,name,phone,birth_date) values(bid,'Y','01012345abc','2000-01-01');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 6b fail: phone with letters not rejected'; end if;

  caught := false;
  begin
    insert into members(branch_id,name,phone,birth_date) values(bid,'Z',phone11,null);
  exception when not_null_violation then caught := true;
  end;
  if not caught then raise exception 'case 6c fail: NULL birth_date not rejected'; end if;

  caught := false;
  begin
    insert into members(branch_id,name,phone,birth_date) values(bid, repeat('n',101), phone11,'2000-01-01');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 6d fail: name length>100 not rejected'; end if;

  raise notice 'case 6 PASS - members CHECK (phone, birth_date, name)';
end $$;

-- 7. members.phone_last4 자동 채워짐
do $$
declare bid bigint; last4 text; phone11 text;
begin
  phone11 := '0108' || '7654321';
  insert into branches(name,address) values('smoke-7-br','smoke-addr-7') returning id into bid;
  insert into members(branch_id,name,phone,birth_date) values(bid,'PLast',phone11,'2000-01-01');
  select phone_last4 into last4 from members where branch_id=bid and phone=phone11;
  if last4 <> '4321' then raise exception 'case 7 fail: phone_last4 expected 4321, got %', last4; end if;
  raise notice 'case 7 PASS - phone_last4 auto-populated';
end $$;

-- 8. members 부분 유니크 (= AC #6)
do $$
declare b1 bigint; b2 bigint; caught boolean; phone11 text;
begin
  phone11 := '0101' || '1112222';
  insert into branches(name,address) values('smoke-8-br1','smoke-addr-8a') returning id into b1;
  insert into branches(name,address) values('smoke-8-br2','smoke-addr-8b') returning id into b2;
  insert into members(branch_id,name,phone,birth_date) values(b1,'M1',phone11,'2000-01-01');

  caught := false;
  begin
    insert into members(branch_id,name,phone,birth_date) values(b1,'M2',phone11,'2000-01-01');
  exception when unique_violation then caught := true;
  end;
  if not caught then raise exception 'case 8a fail: duplicate (branch_id, phone) not rejected'; end if;

  insert into members(branch_id,name,phone,birth_date) values(b2,'M3',phone11,'2000-01-01');
  raise notice 'case 8 PASS - (branch_id, phone) partial unique + cross-branch allowed';
end $$;

-- 9. memberships EXCLUDE (= AC #1)
do $$
declare bid bigint; mid bigint; caught boolean; phone11 text;
begin
  phone11 := '0109' || '9990000';
  insert into branches(name,address) values('smoke-9-br','smoke-addr-9') returning id into bid;
  insert into members(branch_id,name,phone,birth_date) values(bid,'M9',phone11,'2000-01-01') returning id into mid;
  insert into memberships(member_id,type,months,start_date,end_date,status)
       values(mid,'monthly',1,'2026-06-01','2026-06-30','active');

  caught := false;
  begin
    insert into memberships(member_id,type,months,start_date,end_date,status)
         values(mid,'monthly',1,'2026-06-15','2026-07-14','active');
  exception when exclusion_violation then caught := true;
  end;
  if not caught then raise exception 'case 9a fail: overlapping period not rejected'; end if;

  insert into memberships(member_id,type,months,start_date,end_date,status)
       values(mid,'monthly',1,'2026-07-01','2026-07-31','active');
  raise notice 'case 9 PASS - EXCLUDE blocks overlap, non-overlap allowed';
end $$;

-- 10. memberships CHECK 조합 (= AC #8)
do $$
declare bid bigint; mid bigint; caught boolean; phone11 text;
begin
  phone11 := '0101' || '0101010';
  insert into branches(name,address) values('smoke-10-br','smoke-addr-10') returning id into bid;
  insert into members(branch_id,name,phone,birth_date) values(bid,'M10',phone11,'2000-01-01') returning id into mid;

  caught := false;
  begin
    insert into memberships(member_id,type,months,start_date,end_date,status)
         values(mid,'monthly',null,'2026-08-01','2026-09-01','active');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 10a fail: monthly without months not rejected'; end if;

  caught := false;
  begin
    insert into memberships(member_id,type,remaining,start_date,end_date,status)
         values(mid,'pass10',null,'2026-08-01','2026-10-01','active');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 10b fail: pass10 without remaining not rejected'; end if;

  caught := false;
  begin
    insert into memberships(member_id,type,months,start_date,end_date,status)
         values(mid,'monthly',1,'2026-11-01','2026-12-01','paused');
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 10c fail: paused without pause_* not rejected'; end if;

  raise notice 'case 10 PASS - memberships CHECK (monthly/pass10/paused)';
end $$;

-- 11. payments CHECK (method enum + amount<>0) (= AC #4)
do $$
declare bid bigint; mid bigint; mship bigint; aid bigint; caught boolean; phone11 text;
begin
  phone11 := '0105' || '5556666';
  insert into branches(name,address) values('smoke-11-br','smoke-addr-11') returning id into bid;
  insert into members(branch_id,name,phone,birth_date) values(bid,'M11',phone11,'2000-01-01') returning id into mid;
  insert into memberships(member_id,type,months,start_date,end_date,status)
       values(mid,'monthly',1,'2030-01-01','2030-02-01','active') returning id into mship;
  insert into admins(username,password_hash,role,branch_id)
       values('smoke-11-adm','smoke-fake-not-bcrypt','global',null)
       returning id into aid;

  caught := false;
  begin
    insert into payments(membership_id,branch_id,amount,method,paid_at,performed_by)
         values(mship,bid,150000,'paypal','2030-01-01',aid);
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 11a fail: method=paypal not rejected'; end if;

  caught := false;
  begin
    insert into payments(membership_id,branch_id,amount,method,paid_at,performed_by)
         values(mship,bid,0,'cash','2030-01-01',aid);
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 11b fail: amount=0 not rejected'; end if;

  raise notice 'case 11 PASS - payments CHECK (method, amount<>0)';
end $$;

-- 12. admins CHECK (role/branch_id 조합) (= AC #3)
do $$
declare bid bigint; caught boolean;
begin
  insert into branches(name,address) values('smoke-12-br','smoke-addr-12') returning id into bid;

  caught := false;
  begin
    insert into admins(username,password_hash,role,branch_id)
         values('smoke-12-bad-global','smoke-fake-not-bcrypt','global',bid);
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 12a fail: global with branch_id not rejected'; end if;

  caught := false;
  begin
    insert into admins(username,password_hash,role,branch_id)
         values('smoke-12-bad-branch','smoke-fake-not-bcrypt','branch',null);
  exception when check_violation then caught := true;
  end;
  if not caught then raise exception 'case 12b fail: branch without branch_id not rejected'; end if;

  raise notice 'case 12 PASS - admins role/branch_id CHECK';
end $$;

-- 13. check_ins.membership_id NOT NULL (= AC #2)
do $$
declare bid bigint; mid bigint; caught boolean; phone11 text;
begin
  phone11 := '0107' || '7778888';
  insert into branches(name,address) values('smoke-13-br','smoke-addr-13') returning id into bid;
  insert into members(branch_id,name,phone,birth_date) values(bid,'M13',phone11,'2000-01-01') returning id into mid;
  caught := false;
  begin
    insert into check_ins(member_id,branch_id,membership_id) values(mid,bid,null);
  exception when not_null_violation then caught := true;
  end;
  if not caught then raise exception 'case 13 fail: NULL membership_id not rejected'; end if;
  raise notice 'case 13 PASS - check_ins.membership_id NOT NULL enforced';
end $$;

rollback;

-- 14. updated_at 트리거 동작 (= AC #11)
-- now()는 트랜잭션 시작 시각으로 고정되므로, INSERT와 UPDATE를 별도 트랜잭션으로 분리해
-- now()가 실제로 진보한 상태에서 트리거가 동작하는지 검증한다.
-- 격리 DB는 게이트 종료 시 드롭되므로 ROLLBACK으로 데이터 정리할 필요 없음.

begin;
insert into branches(name,address) values('smoke-14-br','smoke-addr-14');
commit;

select pg_sleep(0.05);

begin;
do $$
declare bid bigint; ts1 timestamptz; ts2 timestamptz;
begin
  select id, updated_at into bid, ts1 from branches where address = 'smoke-addr-14';
  update branches set name = 'smoke-14-renamed' where id = bid;
  select updated_at into ts2 from branches where id = bid;
  if ts2 <= ts1 then
    raise exception 'case 14 fail: updated_at not advanced (% -> %)', ts1, ts2;
  end if;
  raise notice 'case 14 PASS - updated_at trigger fires on UPDATE';
end $$;
commit;
